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
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
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

func TestEveryRunsEachPassInItsOwnSpan(t *testing.T) {
	// Arrange
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)

	t.Cleanup(func() {
		otel.SetTracerProvider(previous)

		_ = provider.Shutdown(context.Background())
	})

	passes := make(chan trace.SpanContext, 8)

	job, err := Every("prune-outbox", time.Millisecond, discardLogger(),
		func(ctx context.Context) error {
			select {
			case passes <- trace.SpanFromContext(ctx).SpanContext():
			default:
			}

			return nil
		})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	seen := make([]trace.SpanContext, 0, 2)

	go func() {
		for range 2 {
			seen = append(seen, <-passes)
		}

		cancel()
	}()

	// Act
	require.ErrorIs(t, job.Run(ctx), context.Canceled)

	require.Len(t, seen, 2)

	assert.True(t, seen[0].IsValid(),
		"without a span the job's own queries end up as traces of one statement each")
	assert.NotEqual(t, seen[0].TraceID(), seen[1].TraceID(),
		"each pass is its own unit of work, not one trace growing for the life of the process")

	spans := recorder.Ended()
	require.NotEmpty(t, spans)
	assert.Equal(t, "job prune-outbox", spans[0].Name())
}

func TestEveryMarksThePassThatFailed(t *testing.T) {
	// Arrange
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)

	t.Cleanup(func() {
		otel.SetTracerProvider(previous)

		_ = provider.Shutdown(context.Background())
	})

	boom := errors.New("connection refused")
	ran := make(chan struct{}, 1)

	job, err := Every("reconcile", time.Hour, discardLogger(),
		func(context.Context) error {
			select {
			case ran <- struct{}{}:
			default:
			}

			return boom
		})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-ran

		cancel()
	}()

	// Act
	require.ErrorIs(t, job.Run(ctx), context.Canceled)

	// Assert
	spans := recorder.Ended()
	require.NotEmpty(t, spans,
		"a pass that ran has to leave a span behind, failed or not")

	assert.Equal(t, "Error", spans[0].Status().Code.String())
	assert.NotEmpty(t, spans[0].Events(),
		"a job that keeps failing is the thing you go looking for in Jaeger")
}
