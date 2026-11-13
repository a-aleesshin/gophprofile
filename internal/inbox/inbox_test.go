package inbox

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

func TestInboxChecksItsArgumentsBeforeTouchingTheDatabase(t *testing.T) {
	t.Parallel()

	in := NewInbox(pgtx.New(nil))
	ctx := context.Background()
	effect := func(context.Context) error { return nil }

	tests := map[string]func() error{
		"seen without a consumer": func() error {
			_, err := in.Seen(ctx, "", uuid.New())

			return err
		},
		"seen without a message": func() error {
			_, err := in.Seen(ctx, "file.worker", uuid.Nil)

			return err
		},
		"mark without a consumer": func() error {
			_, err := in.Mark(ctx, "", uuid.New())

			return err
		},
		"mark without a message": func() error {
			_, err := in.Mark(ctx, "file.worker", uuid.Nil)

			return err
		},
		"once without a consumer": func() error {
			_, err := in.Once(ctx, "", uuid.New(), effect)

			return err
		},
		"once without an effect": func() error {
			_, err := in.Once(ctx, "file.worker", uuid.New(), nil)

			return err
		},
		"outside a transaction without a consumer": func() error {
			_, err := in.OnceOutsideTransaction(ctx, "", uuid.New(), effect)

			return err
		},
		"outside a transaction without an effect": func() error {
			_, err := in.OnceOutsideTransaction(ctx, "file.worker", uuid.New(), nil)

			return err
		},
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, call(), ErrInvalidEffect,
				"the pool here is nil, so reaching the database at all would panic")
		})
	}
}

func TestNewPrunerRejectsABrokenConfig(t *testing.T) {
	t.Parallel()

	db := pgtx.New(nil)

	tests := []struct {
		name   string
		db     *pgtx.DB
		clock  Clock
		mutate func(*PrunerConfig)
	}{
		{name: "no db", clock: clock.System{}},
		{name: "no clock", db: db},
		{
			name: "zero retention", db: db, clock: clock.System{},
			mutate: func(c *PrunerConfig) { c.Retention = 0 },
		},
		{
			name: "zero batch size", db: db, clock: clock.System{},
			mutate: func(c *PrunerConfig) { c.BatchSize = 0 },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := PrunerConfig{Retention: time.Hour, BatchSize: 10}
			if test.mutate != nil {
				test.mutate(&cfg)
			}

			// Act
			pruner, err := NewPruner(test.db, test.clock, cfg)

			// Assert
			require.ErrorIs(t, err, ErrInvalidPrunerConfig)
			assert.Nil(t, pruner)
		})
	}
}
