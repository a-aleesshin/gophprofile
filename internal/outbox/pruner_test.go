package outbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

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
