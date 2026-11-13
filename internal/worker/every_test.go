package worker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestEveryRunsImmediatelyAndRepeats(t *testing.T) {
	t.Parallel()

	// Arrange
	var runs atomic.Int32

	job, err := Every("reconcile", 20*time.Millisecond, discardLogger(),
		func(context.Context) error {
			runs.Add(1)

			return nil
		})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())

	go func() { _ = job.Run(ctx) }()

	// Assert
	require.Eventually(t, func() bool { return runs.Load() >= 3 }, 5*time.Second,
		10*time.Millisecond, "the first pass must not wait for the first tick")

	cancel()
}

func TestEveryKeepsGoingAfterAFailedPass(t *testing.T) {
	t.Parallel()

	// Arrange
	var (
		runs   atomic.Int32
		logged bytes.Buffer
	)

	logger := slog.New(slog.NewTextHandler(&logged, nil))

	job, err := Every("reap", 10*time.Millisecond, logger, func(context.Context) error {
		runs.Add(1)

		return errors.New("postgres is busy")
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() { done <- job.Run(ctx) }()

	// Assert
	require.Eventually(t, func() bool { return runs.Load() >= 3 }, 5*time.Second,
		10*time.Millisecond,
		"a periodic pass runs again by its nature, so one failure must not stop it")

	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the job did not stop after the context was cancelled")
	}

	assert.Contains(t, logged.String(), "postgres is busy",
		"a swallowed failure must at least be visible in the log")
	assert.Contains(t, logged.String(), "job=reap")
}

func TestEveryStopsWhenTheContextIsDone(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	job, err := Every("reap", time.Hour, discardLogger(),
		func(ctx context.Context) error { return ctx.Err() })
	require.NoError(t, err)

	// Act
	runErr := job.Run(ctx)

	// Assert
	require.ErrorIs(t, runErr, context.Canceled)
}

func TestEveryRejectsAnIncompleteJob(t *testing.T) {
	t.Parallel()

	noop := func(context.Context) error { return nil }

	tests := []struct {
		name     string
		job      string
		interval time.Duration
		logger   *slog.Logger
		run      func(context.Context) error
	}{
		{name: "no name", interval: time.Second, logger: discardLogger(), run: noop},
		{name: "no interval", job: "reap", logger: discardLogger(), run: noop},
		{name: "no logger", job: "reap", interval: time.Second, run: noop},
		{name: "nothing to run", job: "reap", interval: time.Second, logger: discardLogger()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			job, err := Every(test.job, test.interval, test.logger, test.run)

			// Assert
			require.ErrorIs(t, err, ErrInvalidJob)
			assert.Nil(t, job)
		})
	}
}
