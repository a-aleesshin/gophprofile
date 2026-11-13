package outbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

type stubSender struct{}

func (stubSender) Send(context.Context, Pending) error { return nil }

func validRelayConfig() RelayConfig {
	return RelayConfig{
		BatchSize:              10,
		Interval:               time.Second,
		MinBackoff:             time.Second,
		MaxBackoff:             time.Minute,
		MaxConsecutiveFailures: 3,
		Lease:                  30 * time.Second,
	}
}

func TestNewRelay(t *testing.T) {
	t.Parallel()

	db := pgtx.New(nil)

	t.Run("accepts a sender per transport", func(t *testing.T) {
		t.Parallel()

		for _, transport := range []string{TransportRabbit, TransportKafka} {
			// Act
			relay, err := NewRelay(db, transport, stubSender{}, clock.System{}, validRelayConfig())

			// Assert
			require.NoError(t, err, transport)
			assert.Equal(t, transport, relay.transport)
		}
	})

	tests := []struct {
		name      string
		db        *pgtx.DB
		transport string
		sender    Sender
		clock     Clock
		mutate    func(*RelayConfig)
	}{
		{name: "no db", transport: TransportKafka, sender: stubSender{}, clock: clock.System{}},
		{name: "no transport", db: db, sender: stubSender{}, clock: clock.System{}},
		{
			name: "unknown transport", db: db, transport: "redis",
			sender: stubSender{}, clock: clock.System{},
		},
		{name: "no sender", db: db, transport: TransportKafka, clock: clock.System{}},
		{name: "no clock", db: db, transport: TransportKafka, sender: stubSender{}},
		{
			name: "zero batch size", db: db, transport: TransportKafka,
			sender: stubSender{}, clock: clock.System{},
			mutate: func(c *RelayConfig) { c.BatchSize = 0 },
		},
		{
			name: "negative batch size", db: db, transport: TransportKafka,
			sender: stubSender{}, clock: clock.System{},
			mutate: func(c *RelayConfig) { c.BatchSize = -1 },
		},
		{
			name: "zero interval", db: db, transport: TransportKafka,
			sender: stubSender{}, clock: clock.System{},
			mutate: func(c *RelayConfig) { c.Interval = 0 },
		},
		{
			name: "zero min backoff", db: db, transport: TransportKafka,
			sender: stubSender{}, clock: clock.System{},
			mutate: func(c *RelayConfig) { c.MinBackoff = 0 },
		},
		{
			name: "max backoff below min", db: db, transport: TransportKafka,
			sender: stubSender{}, clock: clock.System{},
			mutate: func(c *RelayConfig) { c.MaxBackoff = c.MinBackoff - 1 },
		},
		{
			name: "zero max consecutive failures", db: db, transport: TransportKafka,
			sender: stubSender{}, clock: clock.System{},
			mutate: func(c *RelayConfig) { c.MaxConsecutiveFailures = 0 },
		},
		{
			name: "zero lease", db: db, transport: TransportKafka,
			sender: stubSender{}, clock: clock.System{},
			mutate: func(c *RelayConfig) { c.Lease = 0 },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			cfg := validRelayConfig()
			if test.mutate != nil {
				test.mutate(&cfg)
			}

			// Act
			relay, err := NewRelay(test.db, test.transport, test.sender, test.clock, cfg)

			// Assert
			require.ErrorIs(t, err, ErrInvalidRelayConfig)
			assert.Nil(t, relay)
		})
	}
}
