package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

var ErrInvalidJob = errors.New("invalid periodic job")

type Job struct {
	name     string
	interval time.Duration
	run      func(ctx context.Context) error
	logger   *slog.Logger
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

	return &Job{name: name, interval: interval, run: run, logger: logger}, nil
}

func (j *Job) Run(ctx context.Context) error {
	timer := time.NewTimer(j.interval)

	defer timer.Stop()

	for {
		if err := j.run(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			j.logger.Error("periodic job failed", "job", j.name, "error", err)
		}

		timer.Reset(j.interval)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}
