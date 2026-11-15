package worker

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"
)

var ErrNoRunners = errors.New("worker has nothing to run")

type Runner interface {
	Run(ctx context.Context) error
}

type named struct {
	name   string
	runner Runner
}

type Worker struct {
	runners []named
}

func New() *Worker {
	return &Worker{}
}

func (w *Worker) Add(name string, runner Runner) *Worker {
	w.runners = append(w.runners, named{name: name, runner: runner})

	return w
}

func (w *Worker) Run(ctx context.Context) error {
	if len(w.runners) == 0 {
		return ErrNoRunners
	}

	for _, entry := range w.runners {
		if entry.name == "" {
			return fmt.Errorf("%w: a runner needs a name", ErrNoRunners)
		}

		if entry.runner == nil {
			return fmt.Errorf("%w: runner %q is nil", ErrNoRunners, entry.name)
		}
	}

	ctx, cancel := context.WithCancel(ctx)

	defer cancel()

	group, ctx := errgroup.WithContext(ctx)

	for _, entry := range w.runners {
		group.Go(func() error {
			defer cancel()

			if err := entry.runner.Run(ctx); err != nil {
				return fmt.Errorf("%s: %w", entry.name, err)
			}

			return nil
		})
	}

	return group.Wait()
}
