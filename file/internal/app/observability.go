package app

import (
	"net/http"
	"net/http/pprof"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/internal/metrics"
)

func reportStorage(result usecase.ObserveStorageResult) {
	usage := make([]metrics.StorageUsage, 0, len(result.Usage))

	for _, entry := range result.Usage {
		usage = append(usage, metrics.StorageUsage{
			Kind:    entry.Kind,
			Bytes:   entry.Bytes,
			Objects: entry.Objects,
		})
	}

	counts := make([]metrics.FileCount, 0, len(result.Counts))

	for _, entry := range result.Counts {
		counts = append(counts, metrics.FileCount{
			Status: entry.Status,
			Count:  entry.Count,
		})
	}

	metrics.SetStorageUsage(usage)
	metrics.SetFileCounts(counts)
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
