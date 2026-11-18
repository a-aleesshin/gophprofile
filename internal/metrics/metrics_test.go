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
	ObserveOutboxTick("kafka", 7, 2, 0)
	ObserveOutboxTick("kafka", 3, 0, 0)

	// Assert
	assert.InDelta(t, 10, testutil.ToFloat64(outboxPublished.WithLabelValues("kafka")), 0.001)
	assert.InDelta(t, 2, testutil.ToFloat64(outboxFailures.WithLabelValues("kafka")), 0.001)
}

func TestOutboxTickKeepsTransportsApart(t *testing.T) {
	// Arrange
	outboxPublished.Reset()

	// Act
	ObserveOutboxTick("kafka", 1, 0, 0)
	ObserveOutboxTick("rabbit", 4, 0, 0)

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
	ObserveOutboxTick("kafka", 0, 0, 0)

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
	ObserveInbox("file.worker", 5, 1, 0, 2, 0)

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
	ObserveOutboxTick("kafka", 1, 1, 0)
	SetOutboxBacklogAge("kafka", time.Second)
	ObserveInbox("file.worker", 1, 0, 0, 0, 0)
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

func TestParkedMessagesAreCountedApartFromFailures(t *testing.T) {
	// Arrange
	outboxFailures.Reset()
	outboxParked.Reset()

	// Act
	ObserveOutboxTick("kafka", 0, 3, 1)

	// Assert
	assert.InDelta(t, 3, testutil.ToFloat64(outboxFailures.WithLabelValues("kafka")), 0.001)
	assert.InDelta(t, 1, testutil.ToFloat64(outboxParked.WithLabelValues("kafka")), 0.001,
		"a parked message needs a human, a failed one only needs patience")
}

func TestNothingParkedCreatesNoSeries(t *testing.T) {
	// Arrange
	outboxParked.Reset()

	// Act
	ObserveOutboxTick("kafka", 1, 1, 0)

	// Assert
	assert.Equal(t, 0, testutil.CollectAndCount(outboxParked))
}

func TestAvatarUploadCountsAndTimesByOutcome(t *testing.T) {
	// Arrange
	avatarUploads.Reset()
	avatarUploadDuration.Reset()

	// Act
	ObserveAvatarUpload("ready", 3*time.Second)
	ObserveAvatarUpload("ready", 5*time.Second)
	ObserveAvatarUpload("failed", time.Second)

	// Assert
	assert.InDelta(t, 2, testutil.ToFloat64(avatarUploads.WithLabelValues("ready")), 0.001)
	assert.InDelta(t, 1, testutil.ToFloat64(avatarUploads.WithLabelValues("failed")), 0.001,
		"a failed upload is a business outcome, not an absence of one")

	assert.Equal(t, 2, testutil.CollectAndCount(avatarUploadDuration),
		"one histogram per outcome, so a slow failure does not hide inside the successes")
}

func TestFileCountsForgetAStatusThatIsGone(t *testing.T) {
	// Arrange
	SetFileCounts([]FileCount{{Status: "processing", Count: 4}, {Status: "ready", Count: 9}})
	require.InDelta(t, 4, testutil.ToFloat64(files.WithLabelValues("processing")), 0.001)

	// Act
	SetFileCounts([]FileCount{{Status: "ready", Count: 9}})

	// Assert
	assert.Equal(t, 1, testutil.CollectAndCount(files),
		"a gauge that keeps a stale series reports four files stuck in processing forever")
	assert.InDelta(t, 9, testutil.ToFloat64(files.WithLabelValues("ready")), 0.001)
}

func TestStorageUsageSplitsOriginalsFromVariants(t *testing.T) {
	// Arrange
	storageBytes.Reset()
	storageObjects.Reset()

	// Act
	SetStorageUsage([]StorageUsage{
		{Kind: "original", Bytes: 173402, Objects: 8},
		{Kind: "variant", Bytes: 222863, Objects: 24},
	})

	// Assert
	assert.InDelta(t, 173402, testutil.ToFloat64(storageBytes.WithLabelValues("original")), 1)
	assert.InDelta(t, 24, testutil.ToFloat64(storageObjects.WithLabelValues("variant")), 1,
		"variants are the bulk of what is stored, so they have to be visible apart")
}

func TestOutboxDepthReportsPendingAndParkedApart(t *testing.T) {
	// Arrange
	outboxDepth.Reset()

	// Act
	SetOutboxDepth("kafka", 12, 3)

	// Assert
	assert.InDelta(t, 12, testutil.ToFloat64(outboxDepth.WithLabelValues("kafka", "pending")), 0.001)
	assert.InDelta(t, 3, testutil.ToFloat64(outboxDepth.WithLabelValues("kafka", "parked")), 0.001,
		"a parked row is not waiting for anything, so counting it as pending would hide the real queue")
}

func TestRegisterPoolExposesWhatTheReaderReports(t *testing.T) {
	// Arrange
	stats := PoolStats{Acquired: 3, Idle: 2, Total: 5, Max: 10, EmptyAcquires: 7}

	// Act
	require.NoError(t, RegisterPool("test-pool", func() PoolStats { return stats }))

	// Assert
	gathered, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	found := map[string]float64{}

	for _, family := range gathered {
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, pair := range metric.GetLabel() {
				labels[pair.GetName()] = pair.GetValue()
			}

			if labels["pool"] != "test-pool" {
				continue
			}

			key := family.GetName()
			if state, tagged := labels["state"]; tagged {
				key += "/" + state
			}

			if gauge := metric.GetGauge(); gauge != nil {
				found[key] = gauge.GetValue()
			}

			if counter := metric.GetCounter(); counter != nil {
				found[key] = counter.GetValue()
			}
		}
	}

	assert.InDelta(t, 3, found["gophprofile_db_pool_connections/acquired"], 0.001)
	assert.InDelta(t, 2, found["gophprofile_db_pool_connections/idle"], 0.001)
	assert.InDelta(t, 10, found["gophprofile_db_pool_connections_max"], 0.001)
	assert.InDelta(t, 7, found["gophprofile_db_pool_empty_acquires_total"], 0.001,
		"an empty acquire is a caller that waited, which is the first sign max conns is too low")
}

func TestRegisterPoolTakesAnAlreadyRegisteredPoolForDone(t *testing.T) {
	// Arrange
	read := func() PoolStats { return PoolStats{} }
	require.NoError(t, RegisterPool("twice", read))

	// Act
	err := RegisterPool("twice", read)

	// Assert
	require.NoError(t, err,
		"the registry is process wide and the integration harnesses build the app several "+
			"times, so a second registration is work already done, not a failure")
}

func TestRegisterPoolRefusesABrokenCall(t *testing.T) {
	require.ErrorIs(t, RegisterPool("", func() PoolStats { return PoolStats{} }), ErrInvalidPool)
	require.ErrorIs(t, RegisterPool("named", nil), ErrInvalidPool)
}

func TestConsumerLagIsKeptPerTopic(t *testing.T) {
	// Arrange
	consumerLag.Reset()

	// Act
	SetConsumerLag("file", []ConsumerLag{
		{Topic: "gophprofile.profile.v1", Lag: 42},
		{Topic: "other.topic", Lag: 0},
	})

	// Assert
	assert.InDelta(t, 42,
		testutil.ToFloat64(consumerLag.WithLabelValues("file", "gophprofile.profile.v1")), 0.001)
	assert.InDelta(t, 0,
		testutil.ToFloat64(consumerLag.WithLabelValues("file", "other.topic")), 0.001)
}

func TestConsumerLagForgetsATopicThatIsGone(t *testing.T) {
	// Arrange
	consumerLag.Reset()

	SetConsumerLag("file", []ConsumerLag{
		{Topic: "gophprofile.profile.v1", Lag: 7},
		{Topic: "retired.topic", Lag: 900},
	})
	require.Equal(t, 2, testutil.CollectAndCount(consumerLag))

	// Act
	SetConsumerLag("file", []ConsumerLag{{Topic: "gophprofile.profile.v1", Lag: 7}})

	// Assert
	assert.Equal(t, 1, testutil.CollectAndCount(consumerLag),
		"серия исчезнувшего топика держала бы девятьсот сообщений лага навсегда, "+
			"и алерт смотрел бы на топик, которого нет")
	assert.InDelta(t, 7,
		testutil.ToFloat64(consumerLag.WithLabelValues("file", "gophprofile.profile.v1")), 0.001)
}

func TestConsumerLagOfOneGroupLeavesAnotherAlone(t *testing.T) {
	// Arrange
	consumerLag.Reset()

	SetConsumerLag("file", []ConsumerLag{{Topic: "gophprofile.profile.v1", Lag: 5}})
	SetConsumerLag("profile", []ConsumerLag{{Topic: "gophprofile.file.v1", Lag: 11}})

	// Act
	SetConsumerLag("file", []ConsumerLag{{Topic: "gophprofile.profile.v1", Lag: 6}})

	// Assert
	assert.InDelta(t, 11,
		testutil.ToFloat64(consumerLag.WithLabelValues("profile", "gophprofile.file.v1")), 0.001,
		"сброс по группе не должен задевать чужие серии: Reset на весь вектор это и сделал бы")
	assert.InDelta(t, 6,
		testutil.ToFloat64(consumerLag.WithLabelValues("file", "gophprofile.profile.v1")), 0.001)
}

func TestConsumerLagDropsAGroupThatDisappeared(t *testing.T) {
	// Arrange
	consumerLag.Reset()

	SetConsumerLag("file", []ConsumerLag{{Topic: "gophprofile.profile.v1", Lag: 3}})

	// Act
	SetConsumerLag("file", nil)

	// Assert
	assert.Equal(t, 0, testutil.CollectAndCount(consumerLag),
		"консьюмер, которого брокер больше не знает, не должен оставлять по себе лаг")
}

func TestInitOutboxMakesTheFirstEventVisibleToIncrease(t *testing.T) {
	// Arrange
	outboxParked.Reset()

	// Act
	InitOutbox("rabbit")

	// Assert
	assert.Equal(t, 1, testutil.CollectAndCount(outboxParked),
		"increase() needs a sample before the event, or the first parked row is invisible to an alert")
	assert.InDelta(t, 0, testutil.ToFloat64(outboxParked.WithLabelValues("rabbit")), 0.001)
}

func TestInitInboxCreatesEveryOutcomeAtZero(t *testing.T) {
	// Arrange
	inboxMessages.Reset()

	// Act
	InitInbox("file.worker")

	// Assert
	assert.Equal(t, 5, testutil.CollectAndCount(inboxMessages))
	assert.InDelta(t, 0,
		testutil.ToFloat64(inboxMessages.WithLabelValues("file.worker", OutcomeExhausted)), 0.001,
		"exhausted is the one an alert watches, so it has to exist before anything is exhausted")
}

func TestObserveStillCreatesNoSeriesOnItsOwn(t *testing.T) {
	// Arrange
	outboxParked.Reset()

	// Act
	ObserveOutboxTick("kafka", 0, 0, 0)

	// Assert
	assert.Equal(t, 0, testutil.CollectAndCount(outboxParked),
		"an idle tick still must not invent a series; only the explicit init does that")
}

func TestStorageUsageForgetsAKindThatIsGone(t *testing.T) {
	// Arrange
	SetStorageUsage([]StorageUsage{
		{Kind: "original", Bytes: 100, Objects: 1},
		{Kind: "variant", Bytes: 40, Objects: 3},
	})
	require.InDelta(t, 40, testutil.ToFloat64(storageBytes.WithLabelValues("variant")), 0.001)

	// Act
	SetStorageUsage([]StorageUsage{{Kind: "original", Bytes: 100, Objects: 1}})

	// Assert
	assert.Equal(t, 1, testutil.CollectAndCount(storageBytes),
		"a kind that stopped reporting must not keep forty bytes on the panel forever")
	assert.Equal(t, 1, testutil.CollectAndCount(storageObjects))
	assert.InDelta(t, 100, testutil.ToFloat64(storageBytes.WithLabelValues("original")), 0.001)
}
