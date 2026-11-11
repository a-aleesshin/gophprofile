package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

var ErrInvalidRelayConfig = errors.New("invalid relay config")

type Pending struct {
	ID           int64
	Transport    string
	Destination  string
	PartitionKey string
	Type         string
	Payload      []byte
	Attempts     int
}

type Sender interface {
	Send(ctx context.Context, message Pending) error
}

type RelayConfig struct {
	BatchSize              int           `env:"BATCH_SIZE" envDefault:"100"`
	Interval               time.Duration `env:"INTERVAL" envDefault:"1s"`
	MinBackoff             time.Duration `env:"MIN_BACKOFF" envDefault:"1s"`
	MaxBackoff             time.Duration `env:"MAX_BACKOFF" envDefault:"30s"`
	MaxConsecutiveFailures int           `env:"MAX_CONSECUTIVE_FAILURES" envDefault:"3"`
}

type RelayStats struct {
	Published int
	Failed    int
}

type Relay struct {
	db        *pgtx.DB
	transport string
	sender    Sender
	clock     Clock
	cfg       RelayConfig
}

func NewRelay(
	db *pgtx.DB,
	transport string,
	sender Sender,
	clock Clock,
	cfg RelayConfig,
) (*Relay, error) {
	switch {
	case db == nil:
		return nil, fmt.Errorf("%w: db is required", ErrInvalidRelayConfig)
	case transport != TransportRabbit && transport != TransportKafka:
		return nil, fmt.Errorf("%w: unknown transport %q", ErrInvalidRelayConfig, transport)
	case sender == nil:
		return nil, fmt.Errorf("%w: sender is required", ErrInvalidRelayConfig)
	case clock == nil:
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidRelayConfig)
	case cfg.BatchSize <= 0:
		return nil, fmt.Errorf("%w: batch size must be positive", ErrInvalidRelayConfig)
	case cfg.Interval <= 0:
		return nil, fmt.Errorf("%w: interval must be positive", ErrInvalidRelayConfig)
	case cfg.MinBackoff <= 0:
		return nil, fmt.Errorf("%w: min backoff must be positive", ErrInvalidRelayConfig)
	case cfg.MaxBackoff < cfg.MinBackoff:
		return nil, fmt.Errorf("%w: max backoff must not be below min backoff", ErrInvalidRelayConfig)
	case cfg.MaxConsecutiveFailures <= 0:
		return nil, fmt.Errorf("%w: max consecutive failures must be positive",
			ErrInvalidRelayConfig)
	}

	return &Relay{db: db, transport: transport, sender: sender, clock: clock, cfg: cfg}, nil
}
