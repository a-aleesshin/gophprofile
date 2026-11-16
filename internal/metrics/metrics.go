package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "gophprofile"

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

	outboxParked = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "outbox_parked_total",
		Help:      "Messages the relay gave up on, which need a human.",
	}, []string{"transport"})

	outboxBacklogAge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "outbox_oldest_unpublished_age_seconds",
		Help:      "Age of the oldest row the relay has not published yet.",
	}, []string{"transport"})

	consumerLag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "kafka_consumer_lag",
		Help:      "Records a consumer group has not read yet, by group and topic.",
	}, []string{"group", "topic"})

	outboxDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "outbox_depth",
		Help:      "Rows waiting to be published, by transport and state.",
	}, []string{"transport", "state"})

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
		Help:      "Calls served, by kind, method and code.",
	}, []string{"kind", "method", "code"})

	avatarUploads = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "avatar_uploads_total",
		Help:      "Avatars that reached a terminal state, by outcome.",
	}, []string{"status"})

	avatarUploadDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "avatar_upload_duration_seconds",
		Help:      "How long an avatar took from the request to its terminal state.",
		Buckets:   []float64{0.5, 1, 2, 5, 10, 30, 60, 300},
	}, []string{"status"})

	storageBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "storage_bytes",
		Help:      "Bytes the service keeps in object storage, by kind.",
	}, []string{"kind"})

	storageObjects = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "storage_objects",
		Help:      "Objects the service keeps in object storage, by kind.",
	}, []string{"kind"})

	files = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "files",
		Help:      "Files the service knows about, by status.",
	}, []string{"status"})

	grpcDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "grpc_request_duration_seconds",
		Help:      "How long a call took, by kind and method.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"kind", "method"})
)

const (
	KindUnary  = "unary"
	KindStream = "stream"
)

const (
	OutcomeHandled   = "handled"
	OutcomeDuplicate = "duplicate"
	OutcomeRetried   = "retried"
	OutcomeDropped   = "dropped"
	OutcomeExhausted = "exhausted"
)

func InitOutbox(transport string) {
	outboxPublished.WithLabelValues(transport).Add(0)
	outboxFailures.WithLabelValues(transport).Add(0)
	outboxParked.WithLabelValues(transport).Add(0)
}

func InitInbox(consumer string) {
	for _, outcome := range []string{
		OutcomeHandled, OutcomeDuplicate, OutcomeRetried, OutcomeDropped, OutcomeExhausted,
	} {
		inboxMessages.WithLabelValues(consumer, outcome).Add(0)
	}
}

func ObserveOutboxTick(transport string, published, failed, parked int) {
	if published > 0 {
		outboxPublished.WithLabelValues(transport).Add(float64(published))
	}

	if failed > 0 {
		outboxFailures.WithLabelValues(transport).Add(float64(failed))
	}

	if parked > 0 {
		outboxParked.WithLabelValues(transport).Add(float64(parked))
	}
}

func SetOutboxBacklogAge(transport string, age time.Duration) {
	outboxBacklogAge.WithLabelValues(transport).Set(age.Seconds())
}

func SetConsumerLag(group, topic string, lag int64) {
	consumerLag.WithLabelValues(group, topic).Set(float64(lag))
}

func SetOutboxDepth(transport string, pending, parked int64) {
	outboxDepth.WithLabelValues(transport, "pending").Set(float64(pending))
	outboxDepth.WithLabelValues(transport, "parked").Set(float64(parked))
}

func ObserveInbox(consumer string, handled, duplicate, retried, dropped, exhausted int) {
	for outcome, count := range map[string]int{
		OutcomeHandled:   handled,
		OutcomeDuplicate: duplicate,
		OutcomeRetried:   retried,
		OutcomeDropped:   dropped,
		OutcomeExhausted: exhausted,
	} {
		if count > 0 {
			inboxMessages.WithLabelValues(consumer, outcome).Add(float64(count))
		}
	}
}

func ObserveAvatarUpload(status string, took time.Duration) {
	avatarUploads.WithLabelValues(status).Inc()
	avatarUploadDuration.WithLabelValues(status).Observe(took.Seconds())
}

type StorageUsage struct {
	Kind    string
	Bytes   int64
	Objects int64
}

func SetStorageUsage(usage []StorageUsage) {
	for _, entry := range usage {
		storageBytes.WithLabelValues(entry.Kind).Set(float64(entry.Bytes))
		storageObjects.WithLabelValues(entry.Kind).Set(float64(entry.Objects))
	}
}

type FileCount struct {
	Status string
	Count  int64
}

func SetFileCounts(counts []FileCount) {
	files.Reset()

	for _, entry := range counts {
		files.WithLabelValues(entry.Status).Set(float64(entry.Count))
	}
}

func ObserveHTTPRequest(route, method string, status int, took time.Duration) {
	if route == "" {
		route = "unmatched"
	}

	httpRequests.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	httpDuration.WithLabelValues(route, method).Observe(took.Seconds())
}

func ObserveGRPCCall(kind, method, code string, took time.Duration) {
	grpcRequests.WithLabelValues(kind, method, code).Inc()
	grpcDuration.WithLabelValues(kind, method).Observe(took.Seconds())
}

func ConsumerLagForTest(group, topic string) prometheus.Gauge {
	return consumerLag.WithLabelValues(group, topic)
}

func BacklogAgeForTest(transport string) prometheus.Gauge {
	return outboxBacklogAge.WithLabelValues(transport)
}
