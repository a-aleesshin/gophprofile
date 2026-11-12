package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "gophprofile"

// Only this package knows about prometheus. Everything else reports through the
// functions below, so the choice of client stays replaceable and no hot path
// grows a collector of its own.
var (
	outboxPublished = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "outbox_published_total",
		Help:      "Messages the relay handed to the broker.",
	}, []string{"transport"})

	outboxFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "outbox_publish_failures_total",
		Help:      "Attempts the relay could not hand to the broker.",
	}, []string{"transport"})

	// The one metric the design calls mandatory: a relay that stops publishing
	// is invisible in every other number, because nothing fails and nothing
	// errors — the rows simply keep piling up.
	outboxBacklogAge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "outbox_oldest_unpublished_age_seconds",
		Help:      "Age of the oldest row the relay has not published yet.",
	}, []string{"transport"})

	inboxMessages = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "inbox_messages_total",
		Help:      "Messages a consumer settled, by outcome.",
	}, []string{"consumer", "outcome"})

	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "http_requests_total",
		Help:      "Requests served, by route and status.",
	}, []string{"route", "method", "status"})

	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_request_duration_seconds",
		Help:      "How long a request took, by route.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"route", "method"})

	grpcRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "grpc_requests_total",
		Help:      "Calls served, by method and code.",
	}, []string{"method", "code"})

	grpcDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "grpc_request_duration_seconds",
		Help:      "How long a call took, by method.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method"})
)

const (
	OutcomeHandled   = "handled"
	OutcomeDuplicate = "duplicate"
	OutcomeRetried   = "retried"
	OutcomeDropped   = "dropped"
)

func ObserveOutboxTick(transport string, published, failed int) {
	if published > 0 {
		outboxPublished.WithLabelValues(transport).Add(float64(published))
	}

	if failed > 0 {
		outboxFailures.WithLabelValues(transport).Add(float64(failed))
	}
}

// SetOutboxBacklogAge takes zero when nothing is waiting, so a drained outbox
// reports 0 instead of keeping the last age it ever saw.
func SetOutboxBacklogAge(transport string, age time.Duration) {
	outboxBacklogAge.WithLabelValues(transport).Set(age.Seconds())
}

func ObserveInbox(consumer string, handled, duplicate, retried, dropped int) {
	for outcome, count := range map[string]int{
		OutcomeHandled:   handled,
		OutcomeDuplicate: duplicate,
		OutcomeRetried:   retried,
		OutcomeDropped:   dropped,
	} {
		if count > 0 {
			inboxMessages.WithLabelValues(consumer, outcome).Add(float64(count))
		}
	}
}

func ObserveHTTPRequest(route, method string, status int, took time.Duration) {
	if route == "" {
		route = "unmatched"
	}

	httpRequests.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	httpDuration.WithLabelValues(route, method).Observe(took.Seconds())
}

func ObserveGRPCCall(method, code string, took time.Duration) {
	grpcRequests.WithLabelValues(method, code).Inc()
	grpcDuration.WithLabelValues(method).Observe(took.Seconds())
}

// BacklogAgeForTest exposes the gauge so the relay's own integration test can
// assert on it without making the collectors package-public.
func BacklogAgeForTest(transport string) prometheus.Gauge {
	return outboxBacklogAge.WithLabelValues(transport)
}
