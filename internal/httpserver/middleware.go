package httpserver

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/a-aleesshin/gophprofile/internal/metrics"
)

type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}

	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	return r.ResponseWriter.Write(body)
}

func (r *statusRecorder) statusOrOK() int {
	if r.status == 0 {
		return http.StatusOK
	}

	return r.status
}

// Recover turns a panic into a 500. New applies it to every server it builds,
// so no handler can take a connection down with it: without recovery net/http
// logs the panic and closes the connection, and the client gets no status at all.
// It answers only when nothing has been written yet — once the status line is
// out, the response cannot be taken back, and the panic is left to the log.
func Recover(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w}

		defer func() {
			panicked := recover()
			if panicked == nil {
				return
			}

			logger.ErrorContext(r.Context(), "http handler panicked",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("route", r.Pattern),
				slog.Any("panic", panicked))

			if recorder.status != 0 {
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)

			_, _ = w.Write([]byte(`{"error":"internal"}` + "\n"))
		}()

		next.ServeHTTP(recorder, r)
	})
}

// Log records one line per request. It belongs inside the mux, registered per
// route, because the route pattern is set on the request only after a match —
// outside the mux there is nothing but the raw path, and the path carries ids.
func Log(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(recorder, r)

		status := recorder.statusOrOK()

		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		}

		took := time.Since(started)

		metrics.ObserveHTTPRequest(r.Pattern, r.Method, status, took)

		logger.Log(r.Context(), level, "http request",
			slog.String("method", r.Method),
			slog.String("route", r.Pattern),
			slog.Int("status", status),
			slog.Duration("took", took))
	})
}
