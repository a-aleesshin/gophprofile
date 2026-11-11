package outbox

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
