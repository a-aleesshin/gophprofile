package outbox

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

func TestScopeOf(t *testing.T) {
	t.Parallel()

	t.Run("a partition key is the scope", func(t *testing.T) {
		t.Parallel()

		// Act
		first := scopeOf(claimed{Pending: Pending{ID: 7, PartitionKey: "file-1"}})
		second := scopeOf(claimed{Pending: Pending{ID: 8, PartitionKey: "file-1"}})

		// Assert
		assert.Equal(t, first, second, "the id must not take part in a keyed scope")
	})

	t.Run("without a key every message is its own scope", func(t *testing.T) {
		t.Parallel()

		// Act
		first := scopeOf(claimed{Pending: Pending{ID: 7}})
		second := scopeOf(claimed{Pending: Pending{ID: 8}})

		// Assert
		assert.NotEqual(t, first, second,
			"a message without a key must not block any other message")
	})

	t.Run("a key never collides with a generated scope", func(t *testing.T) {
		t.Parallel()

		// Act
		keyed := scopeOf(claimed{Pending: Pending{ID: 7, PartitionKey: "id:8"}})
		generated := scopeOf(claimed{Pending: Pending{ID: 8}})

		// Assert
		assert.NotEqual(t, keyed, generated)
	})
}

func TestBackoffFor(t *testing.T) {
	t.Parallel()

	const (
		minDelay = time.Second
		maxDelay = 5 * time.Minute
	)

	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{attempts: 0, want: time.Second},
		{attempts: 1, want: 2 * time.Second},
		{attempts: 2, want: 4 * time.Second},
		{attempts: 8, want: 256 * time.Second},
		{attempts: 9, want: 5 * time.Minute},
		{attempts: 100, want: 5 * time.Minute},
		{attempts: math.MaxInt32, want: 5 * time.Minute},
	}

	for _, test := range tests {
		// Act
		got := backoffFor(test.attempts, minDelay, maxDelay)

		// Assert
		assert.Equal(t, test.want, got, "attempts %d", test.attempts)
	}
}

func TestBackoffForClampsAMinimumAboveTheMaximum(t *testing.T) {
	t.Parallel()

	// Act
	got := backoffFor(0, time.Minute, time.Second)

	// Assert
	assert.Equal(t, time.Second, got)
}

func TestBackoffForNeverOverflows(t *testing.T) {
	t.Parallel()

	// Act
	got := backoffFor(math.MaxInt32, time.Hour, time.Duration(math.MaxInt64))

	// Assert
	assert.Equal(t, time.Duration(math.MaxInt64), got)
}

type scriptedSender struct {
	refuse map[int64]error
	sent   []int64
}

func (s *scriptedSender) Send(_ context.Context, message Pending) error {
	if err := s.refuse[message.ID]; err != nil {
		return err
	}

	s.sent = append(s.sent, message.ID)

	return nil
}

func publishingRelay(t *testing.T, sender Sender, maxFailures int) *Relay {
	t.Helper()

	cfg := validRelayConfig()
	cfg.MaxConsecutiveFailures = maxFailures

	relay, err := NewRelay(pgtx.New(nil), TransportKafka, sender, clock.System{}, cfg)
	require.NoError(t, err)

	return relay
}

func claimedOf(id int64, key string) claimed {
	return claimed{Pending: Pending{
		ID:           id,
		Transport:    TransportKafka,
		Destination:  "gophprofile.file.v1",
		PartitionKey: key,
		Type:         "FileProcessed",
	}}
}

func idsOf(failures []failure) []int64 {
	ids := make([]int64, 0, len(failures))
	for _, item := range failures {
		ids = append(ids, item.message.ID)
	}

	return ids
}

func TestUntouchedOfKeepsWhatNobodyDecidedOn(t *testing.T) {
	t.Parallel()

	leased := []claimed{claimedOf(1, "a"), claimedOf(2, "a"), claimedOf(3, "b")}

	tests := []struct {
		name      string
		published []int64
		failures  []failure
		want      []int64
	}{
		{name: "nothing settled", want: []int64{1, 2, 3}},
		{name: "all published", published: []int64{1, 2, 3}},
		{
			name:     "one failed, the rest of its scope untouched",
			failures: []failure{{message: claimedOf(1, "a")}},
			want:     []int64{2, 3},
		},
		{
			name:      "mixed",
			published: []int64{3},
			failures:  []failure{{message: claimedOf(1, "a")}},
			want:      []int64{2},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			untouched := untouchedOf(leased, test.published, test.failures)

			ids := make([]int64, 0, len(untouched))
			for _, message := range untouched {
				ids = append(ids, message.ID)
			}

			assert.Equal(t, test.want, nonEmpty(ids),
				"anything left here has its schedule restored, so a miss would stall the row")
		})
	}
}

func nonEmpty(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}

	return ids
}

func TestPublishHoldsTheRestOfAScopeAfterAFailure(t *testing.T) {
	t.Parallel()

	// Arrange
	sender := &scriptedSender{refuse: map[int64]error{1: errors.New("broker said no")}}
	relay := publishingRelay(t, sender, 3)

	leased := []claimed{claimedOf(1, "user-a"), claimedOf(2, "user-a"), claimedOf(3, "user-b")}

	// Act
	published, failures, err := relay.publish(context.Background(), leased)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, []int64{3}, published,
		"another scope is independent and must not wait")
	assert.Equal(t, []int64{1}, idsOf(failures))
	assert.NotContains(t, sender.sent, int64(2),
		"letting the next message of the same key through would reorder the stream")
}

func TestPublishResetsTheRunOfFailuresOnSuccess(t *testing.T) {
	t.Parallel()

	// Arrange
	sender := &scriptedSender{refuse: map[int64]error{
		1: errors.New("broker said no"),
		3: errors.New("broker said no"),
	}}
	relay := publishingRelay(t, sender, 2)

	leased := []claimed{
		claimedOf(1, "a"), claimedOf(2, "b"), claimedOf(3, "c"), claimedOf(4, "d"),
	}

	// Act
	published, failures, err := relay.publish(context.Background(), leased)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, []int64{2, 4}, published,
		"two failures in a row stop the tick, but a success in between is not a row")
	assert.Equal(t, []int64{1, 3}, idsOf(failures))
}

func TestPublishStopsAfterTooManyFailuresInARow(t *testing.T) {
	t.Parallel()

	// Arrange
	sender := &scriptedSender{refuse: map[int64]error{
		1: errors.New("broker said no"),
		2: errors.New("broker said no"),
		3: errors.New("broker said no"),
	}}
	relay := publishingRelay(t, sender, 2)

	leased := []claimed{claimedOf(1, "a"), claimedOf(2, "b"), claimedOf(3, "c")}

	// Act
	published, failures, err := relay.publish(context.Background(), leased)

	// Assert
	require.NoError(t, err)
	assert.Empty(t, published)
	assert.Equal(t, []int64{1, 2}, idsOf(failures),
		"a broker that refuses everything must not cost a round trip per leased row")
}

func TestPublishReportsACancelledContext(t *testing.T) {
	t.Parallel()

	// Arrange
	sender := &scriptedSender{refuse: map[int64]error{2: errors.New("broker said no")}}
	relay := publishingRelay(t, sender, 3)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	published, failures, err := relay.publish(ctx, []claimed{claimedOf(1, "a"), claimedOf(2, "b")})

	// Assert
	require.ErrorIs(t, err, context.Canceled,
		"shutdown is not a broker failure and must not count as an attempt")
	assert.Empty(t, failures)
	assert.Equal(t, []int64{1}, published,
		"what did go out before the cancellation still has to be marked published")
}
