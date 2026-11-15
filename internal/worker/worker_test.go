package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runnerFunc func(ctx context.Context) error

func (f runnerFunc) Run(ctx context.Context) error { return f(ctx) }

func blockingRunner(started *atomic.Int32) Runner {
	return runnerFunc(func(ctx context.Context) error {
		started.Add(1)
		<-ctx.Done()

		return ctx.Err()
	})
}

func TestWorkerRunsNothing(t *testing.T) {
	t.Parallel()

	// Act
	err := New().Run(t.Context())

	// Assert
	require.ErrorIs(t, err, ErrNoRunners)
}

func TestWorkerRejectsAnIncompleteRunner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		worker *Worker
	}{
		{name: "no name", worker: New().Add("", runnerFunc(func(context.Context) error { return nil }))},
		{name: "nil runner", worker: New().Add("relay", nil)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			err := test.worker.Run(t.Context())

			// Assert
			require.ErrorIs(t, err, ErrNoRunners)
		})
	}
}

func TestWorkerStopsEveryoneWhenOneFails(t *testing.T) {
	t.Parallel()

	// Arrange
	var started atomic.Int32

	broken := errors.New("kafka is unreachable")

	w := New().
		Add("rabbit relay", blockingRunner(&started)).
		Add("kafka relay", runnerFunc(func(context.Context) error {
			started.Add(1)

			return broken
		})).
		Add("commands", blockingRunner(&started))

	// Act
	err := w.Run(t.Context())

	// Assert
	require.ErrorIs(t, err, broken)
	assert.ErrorContains(t, err, "kafka relay", "the failing runner must be named")
	assert.Equal(t, int32(3), started.Load())
}

func TestWorkerStopsEveryoneWhenOneReturnsCleanly(t *testing.T) {
	t.Parallel()

	// Arrange
	var started atomic.Int32

	w := New().
		Add("relay", blockingRunner(&started)).
		Add("one shot", runnerFunc(func(context.Context) error { return nil }))

	done := make(chan error, 1)

	// Act
	go func() { done <- w.Run(t.Context()) }()

	// Assert
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled,
			"a runner that exits must not leave the others running")
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after a runner exited")
	}
}

func TestWorkerStopsWhenTheContextIsDone(t *testing.T) {
	t.Parallel()

	// Arrange
	var started atomic.Int32

	ctx, cancel := context.WithCancel(t.Context())

	w := New().
		Add("relay", blockingRunner(&started)).
		Add("commands", blockingRunner(&started))

	done := make(chan error, 1)

	go func() { done <- w.Run(ctx) }()

	// Act
	cancel()

	// Assert
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}
