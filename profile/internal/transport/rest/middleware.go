package rest

import (
	"log/slog"
	"net/http"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status

	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	return r.ResponseWriter.Write(body)
}

func recovery(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			panicked := recover()
			if panicked == nil {
				return
			}

			logger.ErrorContext(r.Context(), "http handler panicked",
				slog.String("method", r.Method),
				slog.String("route", r.Pattern),
				slog.Any("panic", panicked))

			writeError(w, http.StatusInternalServerError, "internal")
		}()

		next.ServeHTTP(w, r)
	})
}

func logging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(recorder, r)

		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}

		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		}

		logger.Log(r.Context(), level, "http request",
			slog.String("method", r.Method),
			slog.String("route", r.Pattern),
			slog.Int("status", status),
			slog.Duration("took", time.Since(started)))
	})
}
