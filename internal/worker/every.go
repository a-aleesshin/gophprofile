package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/a-aleesshin/gophprofile/internal/worker"

var ErrInvalidJob = errors.New("invalid periodic job")

type Job struct {
	name     string
	interval time.Duration
	run      func(ctx context.Context) error
	logger   *slog.Logger
	tracer   trace.Tracer
}

func Every(
	name string,
	interval time.Duration,
	logger *slog.Logger,
	run func(ctx context.Context) error,
) (*Job, error) {
	switch {
	case name == "":
		return nil, fmt.Errorf("%w: name is required", ErrInvalidJob)
	case interval <= 0:
		return nil, fmt.Errorf("%w: interval must be positive", ErrInvalidJob)
	case logger == nil:
		return nil, fmt.Errorf("%w: logger is required", ErrInvalidJob)
	case run == nil:
		return nil, fmt.Errorf("%w: nothing to run", ErrInvalidJob)
	}

	return &Job{
		name:     name,
		interval: interval,
		run:      run,
		logger:   logger,
		tracer:   otel.Tracer(tracerName),
	}, nil
}

func (j *Job) once(ctx context.Context) error {
	ctx, span := j.tracer.Start(ctx, "job "+j.name, trace.WithSpanKind(trace.SpanKindInternal))

	defer span.End()

	err := j.run(ctx)

	if err != nil && span.IsRecording() {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return err
}

func (j *Job) Run(ctx context.Context) error {
	timer := time.NewTimer(j.interval)

	defer timer.Stop()

	for {
		if err := j.once(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			j.logger.ErrorContext(ctx, "periodic job failed", "job", j.name, "error", err)
		}

		timer.Reset(j.interval)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}
