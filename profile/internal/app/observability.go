package app

import (
	"net/http"
	"net/http/pprof"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/a-aleesshin/gophprofile/internal/metrics"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

func observeOutcomes(outcomes []usecase.AvatarOutcome) {
	for _, outcome := range outcomes {
		metrics.ObserveAvatarUpload(outcome.Status.String(), outcome.Took)
	}
}

func metricsHandler() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /metrics", promhttp.Handler())

	return mux
}

func pprofHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)

	return mux
}
