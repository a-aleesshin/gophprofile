package httpserver

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
