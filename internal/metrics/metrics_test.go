package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutboxTickCountsBothOutcomes(t *testing.T) {
	// Arrange
	outboxPublished.Reset()
	outboxFailures.Reset()

	// Act
	ObserveOutboxTick("kafka", 7, 2)
	ObserveOutboxTick("kafka", 3, 0)

	// Assert
	assert.InDelta(t, 10, testutil.ToFloat64(outboxPublished.WithLabelValues("kafka")), 0.001)
	assert.InDelta(t, 2, testutil.ToFloat64(outboxFailures.WithLabelValues("kafka")), 0.001)
}

func TestOutboxTickKeepsTransportsApart(t *testing.T) {
	// Arrange
	outboxPublished.Reset()

	// Act
	ObserveOutboxTick("kafka", 1, 0)
	ObserveOutboxTick("rabbit", 4, 0)

	// Assert
	assert.InDelta(t, 1, testutil.ToFloat64(outboxPublished.WithLabelValues("kafka")), 0.001)
	assert.InDelta(t, 4, testutil.ToFloat64(outboxPublished.WithLabelValues("rabbit")), 0.001,
		"one stuck transport must be visible while the other keeps working")
}

func TestAnEmptyTickCreatesNoSeries(t *testing.T) {
	// Arrange
	outboxPublished.Reset()
	outboxFailures.Reset()

	// Act
	ObserveOutboxTick("kafka", 0, 0)

	// Assert
	assert.Equal(t, 0, testutil.CollectAndCount(outboxPublished),
		"an idle relay ticks every second and must not invent series")
	assert.Equal(t, 0, testutil.CollectAndCount(outboxFailures))
}

func TestBacklogAgeFallsBackToZero(t *testing.T) {
	// Arrange
	outboxBacklogAge.Reset()

	// Act
	SetOutboxBacklogAge("kafka", 90*time.Second)
	first := testutil.ToFloat64(outboxBacklogAge.WithLabelValues("kafka"))

	SetOutboxBacklogAge("kafka", 0)

	// Assert
	assert.InDelta(t, 90, first, 0.001)
	assert.InDelta(t, 0, testutil.ToFloat64(outboxBacklogAge.WithLabelValues("kafka")), 0.001,
		"a drained outbox must not keep reporting the last age it saw")
}

func TestInboxCountsOnlyWhatHappened(t *testing.T) {
	// Arrange
	inboxMessages.Reset()

	// Act
	ObserveInbox("file.worker", 5, 1, 0, 2)

	// Assert
	assert.InDelta(t, 5,
		testutil.ToFloat64(inboxMessages.WithLabelValues("file.worker", OutcomeHandled)), 0.001)
	assert.InDelta(t, 1,
		testutil.ToFloat64(inboxMessages.WithLabelValues("file.worker", OutcomeDuplicate)), 0.001)
	assert.InDelta(t, 2,
		testutil.ToFloat64(inboxMessages.WithLabelValues("file.worker", OutcomeDropped)), 0.001)
	assert.Equal(t, 3, testutil.CollectAndCount(inboxMessages),
		"retried stayed at zero and must not appear at all")
}

func TestHTTPRequestIsCountedAndTimed(t *testing.T) {
	// Arrange
	httpRequests.Reset()
	httpDuration.Reset()

	// Act
	ObserveHTTPRequest("GET /api/v1/avatars/{avatar_id}", "GET", 302, 12*time.Millisecond)

	// Assert
	assert.InDelta(t, 1, testutil.ToFloat64(httpRequests.WithLabelValues(
		"GET /api/v1/avatars/{avatar_id}", "GET", "302")), 0.001)
	assert.Equal(t, 1, testutil.CollectAndCount(httpDuration))
}

func TestAnUnmatchedRequestGetsAStableLabel(t *testing.T) {
	// Arrange
	httpRequests.Reset()

	// Act
	ObserveHTTPRequest("", "GET", 404, time.Millisecond)

	// Assert
	assert.InDelta(t, 1,
		testutil.ToFloat64(httpRequests.WithLabelValues("unmatched", "GET", "404")), 0.001,
		"an empty label would collapse every unknown path into one unnamed series")
}

func TestGRPCCallIsCountedByCode(t *testing.T) {
	// Arrange
	grpcRequests.Reset()

	// Act
	ObserveGRPCCall(KindUnary,
		"/gophprofile.file.v1.FileService/GetFile", "NotFound", time.Millisecond)
	ObserveGRPCCall(KindUnary,
		"/gophprofile.file.v1.FileService/GetFile", "OK", time.Millisecond)
	ObserveGRPCCall(KindStream,
		"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo", "OK", time.Minute)

	// Assert
	assert.InDelta(t, 1, testutil.ToFloat64(grpcRequests.WithLabelValues(
		KindUnary, "/gophprofile.file.v1.FileService/GetFile", "NotFound")), 0.001)
	assert.InDelta(t, 1, testutil.ToFloat64(grpcRequests.WithLabelValues(
		KindUnary, "/gophprofile.file.v1.FileService/GetFile", "OK")), 0.001)
	assert.InDelta(t, 1, testutil.ToFloat64(grpcRequests.WithLabelValues(
		KindStream, "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo", "OK")), 0.001,
		"a stream keeps its own series so its minutes cannot move the unary quantiles")
}

func TestEverySeriesIsNamespaced(t *testing.T) {
	// Arrange
	ObserveOutboxTick("kafka", 1, 1)
	SetOutboxBacklogAge("kafka", time.Second)
	ObserveInbox("file.worker", 1, 0, 0, 0)
	ObserveHTTPRequest("GET /healthz", "GET", 200, time.Millisecond)
	ObserveGRPCCall(KindUnary, "/x/Y", "OK", time.Millisecond)

	// Act
	problems, err := testutil.GatherAndLint(prometheus.DefaultGatherer)

	// Assert
	require.NoError(t, err)
	assert.Empty(t, problems, "the linter must not complain about our metric names")

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	exposedNames := make(map[string]bool, len(families))
	for _, family := range families {
		exposedNames[family.GetName()] = true
	}

	for _, name := range []string{
		"gophprofile_outbox_published_total",
		"gophprofile_outbox_publish_failures_total",
		"gophprofile_outbox_oldest_unpublished_age_seconds",
		"gophprofile_inbox_messages_total",
		"gophprofile_http_requests_total",
		"gophprofile_http_request_duration_seconds",
		"gophprofile_grpc_requests_total",
		"gophprofile_grpc_request_duration_seconds",
	} {
		assert.True(t, strings.HasPrefix(name, namespace+"_"), name)
		assert.True(t, exposedNames[name], "%s is not exposed on /metrics", name)
	}
}
