package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func captureLogs() (*slog.Logger, *bytes.Buffer) {
	var buffer bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return logger, &buffer
}

func lastRecord(t *testing.T, buffer *bytes.Buffer) map[string]any {
	t.Helper()

	lines := bytes.Split(bytes.TrimSpace(buffer.Bytes()), []byte("\n"))
	require.NotEmpty(t, lines[0], "nothing was logged")

	var record map[string]any

	require.NoError(t, json.Unmarshal(lines[len(lines)-1], &record))

	return record
}

func serve(handler http.Handler, target string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("GET /things/{id}", handler)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))

	return recorder
}

func TestRecoverTurnsAPanicIntoAStatus(t *testing.T) {
	t.Parallel()

	// Arrange
	logger, logs := captureLogs()

	handler := Recover(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("the projection blew up")
	}))

	// Act
	recorder := serve(handler, "/things/7")

	// Assert
	require.Equal(t, http.StatusInternalServerError, recorder.Code,
		"without this the client gets a dropped connection and no status at all")
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"error":"internal"}`, recorder.Body.String())

	record := lastRecord(t, logs)
	assert.Equal(t, "http handler panicked", record["msg"])
	assert.Equal(t, "ERROR", record["level"])
	assert.Equal(t, "/things/7", record["path"])
	assert.Equal(t, "GET /things/{id}", record["route"])
	assert.Contains(t, record["panic"], "the projection blew up")
}

func TestRecoverLeavesAStartedResponseAlone(t *testing.T) {
	t.Parallel()

	// Arrange: a streaming handler may panic after the status line is out.
	logger, logs := captureLogs()

	handler := Recover(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("half a variant"))

		panic("s3 closed the body")
	}))

	// Act
	recorder := serve(handler, "/things/7")

	// Assert
	assert.Equal(t, http.StatusOK, recorder.Code,
		"the status line is already on the wire and cannot be taken back")
	assert.Equal(t, "half a variant", recorder.Body.String(),
		"a json error appended to half a jpeg would be worse than a truncated jpeg")
	assert.Equal(t, "http handler panicked", lastRecord(t, logs)["msg"])
}

func TestRecoverDoesNotTouchANormalResponse(t *testing.T) {
	t.Parallel()

	logger, logs := captureLogs()

	handler := Recover(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	recorder := serve(handler, "/things/7")

	assert.Equal(t, http.StatusTeapot, recorder.Code)
	assert.Empty(t, logs.String(), "a request that did not panic says nothing here")
}

func TestLogRecordsTheRouteNotThePath(t *testing.T) {
	t.Parallel()

	// Arrange
	logger, logs := captureLogs()

	handler := Log(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	// Act
	serve(handler, "/things/0193abcd-ef01-7a23-8b45-0123456789ab")

	// Assert
	record := lastRecord(t, logs)
	assert.Equal(t, "http request", record["msg"])
	assert.Equal(t, "INFO", record["level"])
	assert.Equal(t, "GET", record["method"])
	assert.Equal(t, "GET /things/{id}", record["route"],
		"the pattern groups the logs; the path would carry one id per line")
	assert.Equal(t, float64(http.StatusNoContent), record["status"])
	assert.NotNil(t, record["took"])
}

func TestLogCountsAnImplicitTwoHundred(t *testing.T) {
	t.Parallel()

	logger, logs := captureLogs()

	handler := Log(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("body without an explicit status"))
	}))

	serve(handler, "/things/7")

	assert.Equal(t, float64(http.StatusOK), lastRecord(t, logs)["status"])
}

func TestLogRaisesTheLevelForOurOwnFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		level  string
	}{
		{status: http.StatusNotFound, level: "INFO"},
		{status: http.StatusConflict, level: "INFO"},
		{status: http.StatusInternalServerError, level: "ERROR"},
		{status: http.StatusBadGateway, level: "ERROR"},
	}

	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			t.Parallel()

			logger, logs := captureLogs()

			handler := Log(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))

			serve(handler, "/things/7")

			assert.Equal(t, test.level, lastRecord(t, logs)["level"],
				"a client mistake is not an incident, ours is")
		})
	}
}

func TestNewWrapsEveryServerInRecovery(t *testing.T) {
	t.Parallel()

	// Arrange
	logger, _ := captureLogs()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) {
		panic("nobody wrapped me")
	})

	server, err := New("test", mux, Config{Addr: "127.0.0.1:0", ShutdownTimeout: 1}, logger)
	require.NoError(t, err)

	// Act
	recorder := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/boom", nil))

	// Assert
	assert.Equal(t, http.StatusInternalServerError, recorder.Code,
		"health, metrics and pprof are built the same way and must not be an exception")
}

func counterValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	for _, family := range families {
		if family.GetName() != name {
			continue
		}

		for _, metric := range family.GetMetric() {
			matched := 0

			for _, pair := range metric.GetLabel() {
				if labels[pair.GetName()] == pair.GetValue() {
					matched++
				}
			}

			if matched == len(labels) {
				return metric.GetCounter().GetValue()
			}
		}
	}

	return 0
}

func TestLogFeedsTheMetrics(t *testing.T) {
	// Not parallel: it compares a counter before and after.
	logger, _ := captureLogs()

	const route = "GET /things/{id}"

	labels := map[string]string{"route": route, "method": "GET", "status": "204"}
	before := counterValue(t, "gophprofile_http_requests_total", labels)

	handler := Log(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	serve(handler, "/things/7")

	assert.InDelta(t, before+1,
		counterValue(t, "gophprofile_http_requests_total", labels), 0.001,
		"the log line and the counter come from the same place, so neither can drift")
}

type countingWriter struct {
	*httptest.ResponseRecorder

	writes    int
	readFroms int
}

func (w *countingWriter) Write(body []byte) (int, error) {
	w.writes++

	return w.ResponseRecorder.Write(body)
}

func (w *countingWriter) ReadFrom(src io.Reader) (int64, error) {
	w.readFroms++

	return io.Copy(w.Body, src)
}

type plainReader struct{ io.Reader }

func bigBody() (io.Reader, int) {
	payload := bytes.Repeat([]byte("variant"), 32<<10)

	return plainReader{bytes.NewReader(payload)}, len(payload)
}

func TestRecorderKeepsTheFastCopyPath(t *testing.T) {
	t.Parallel()

	// Arrange
	inner := &countingWriter{ResponseRecorder: httptest.NewRecorder()}
	recorder := &statusRecorder{ResponseWriter: inner}
	src, size := bigBody()

	// Act
	written, err := io.Copy(recorder, src)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, int64(size), written)

	assert.Equal(t, 1, inner.readFroms,
		"hiding ReadFrom costs the sendfile path and one syscall per 32 KiB instead")
	assert.Zero(t, inner.writes)

	assert.Equal(t, size, inner.Body.Len())
	assert.Equal(t, http.StatusOK, recorder.statusOrOK(),
		"a body copied this way still has to be logged as a 200")
}

func TestRecorderCopiesWhenTheWriterCannotReadFrom(t *testing.T) {
	t.Parallel()

	// Arrange
	inner := httptest.NewRecorder()
	recorder := &statusRecorder{ResponseWriter: inner}
	src, size := bigBody()

	// Act
	written, err := io.Copy(recorder, src)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, int64(size), written)
	assert.Equal(t, size, inner.Body.Len())
	assert.Equal(t, http.StatusOK, recorder.statusOrOK())
}

func TestTheServedChainKeepsTheFastCopyPath(t *testing.T) {
	t.Parallel()

	// Arrange
	logger, _ := captureLogs()

	src, size := bigBody()

	mux := http.NewServeMux()
	mux.Handle("GET /things/{id}", Log(logger,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, err := io.Copy(w, src)
			require.NoError(t, err)
		})))

	inner := &countingWriter{ResponseRecorder: httptest.NewRecorder()}

	// Act
	Recover(logger, mux).ServeHTTP(inner, httptest.NewRequest(http.MethodGet, "/things/7", nil))

	// Assert
	assert.Equal(t, 1, inner.readFroms,
		"two nested recorders must both delegate, or the gain is lost on the way down")
	assert.Zero(t, inner.writes)
	assert.Equal(t, size, inner.Body.Len())
}

func TestRecorderLetsTheResponseControllerThrough(t *testing.T) {
	t.Parallel()

	// Arrange
	inner := httptest.NewRecorder()
	recorder := &statusRecorder{ResponseWriter: inner}

	// Act
	err := http.NewResponseController(recorder).Flush()

	// Assert
	require.NoError(t, err,
		"without Unwrap the controller cannot see the real writer and reports ErrNotSupported")
	assert.True(t, inner.Flushed)
}

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)

		_ = provider.Shutdown(context.Background())
	})

	return recorder
}

func TestTraceNamesTheSpanAfterTheRoute(t *testing.T) {
	// Arrange
	recorder := recordSpans(t)

	handler := Trace(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	// Act
	serve(handler, "/things/0193abcd-ef01-7a23-8b45-0123456789ab")

	// Assert
	spans := recorder.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /things/{id}", spans[0].Name(),
		"the path carries an id, so naming spans after it gives Jaeger one operation per avatar")
}

func TestTraceHandsTheSpanToTheHandler(t *testing.T) {
	// Arrange
	recordSpans(t)

	var seen trace.SpanContext

	handler := Trace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = trace.SpanFromContext(r.Context()).SpanContext()

		w.WriteHeader(http.StatusNoContent)
	}))

	// Act
	serve(handler, "/things/7")

	// Assert
	assert.True(t, seen.IsValid(),
		"everything downstream reads the span out of the request context")
	assert.True(t, seen.IsSampled())
}

func TestTraceDoesNotLetACallerPickOurTraceID(t *testing.T) {
	// Arrange
	recorder := recordSpans(t)

	handler := Trace(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	const (
		foreignTrace = "11111111111111111111111111111111"
		foreignSpan  = "2222222222222222"
	)

	mux := http.NewServeMux()
	mux.Handle("GET /things/{id}", handler)

	request := httptest.NewRequest(http.MethodGet, "/things/7", nil)
	request.Header.Set("traceparent", "00-"+foreignTrace+"-"+foreignSpan+"-01")

	// Act
	mux.ServeHTTP(httptest.NewRecorder(), request)

	// Assert
	spans := recorder.Ended()
	require.Len(t, spans, 1)

	assert.NotEqual(t, foreignTrace, spans[0].SpanContext().TraceID().String(),
		"this endpoint faces the internet, so a caller must not be able to join or poison our traces")

	require.Len(t, spans[0].Links(), 1,
		"the incoming context is not trusted as a parent but it is not thrown away either")
	assert.Equal(t, foreignTrace, spans[0].Links()[0].SpanContext.TraceID().String())
}
