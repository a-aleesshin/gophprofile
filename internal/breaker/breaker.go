package breaker

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/a-aleesshin/gophprofile/internal/metrics"
)

var ErrOpen = errors.New("circuit breaker is open")

var ErrInvalidConfig = errors.New("invalid circuit breaker config")

type Config struct {
	Enabled  bool          `env:"BREAKER_ENABLED" envDefault:"true"`
	Failures int           `env:"BREAKER_FAILURES" envDefault:"5"`
	Cooldown time.Duration `env:"BREAKER_COOLDOWN" envDefault:"10s"`
	Probes   int           `env:"BREAKER_PROBES" envDefault:"1"`
}

type Clock interface {
	Now() time.Time
}

type State int

const (
	StateClosed State = iota
	StateHalfOpen
	StateOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateHalfOpen:
		return "half-open"
	case StateOpen:
		return "open"
	default:
		return "unknown"
	}
}

type Breaker struct {
	name     string
	failures int
	cooldown time.Duration
	probes   int
	clock    Clock

	mu       sync.Mutex
	state    State
	strikes  int
	openedAt time.Time
	inFlight int
}

func New(name string, cfg Config, clk Clock) (*Breaker, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	switch {
	case name == "":
		return nil, fmt.Errorf("%w: name is required", ErrInvalidConfig)
	case clk == nil:
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	case cfg.Failures <= 0:
		return nil, fmt.Errorf("%w: failures must be positive", ErrInvalidConfig)
	case cfg.Cooldown <= 0:
		return nil, fmt.Errorf("%w: cooldown must be positive", ErrInvalidConfig)
	case cfg.Probes <= 0:
		return nil, fmt.Errorf("%w: probes must be positive", ErrInvalidConfig)
	}

	metrics.InitBreaker(name)

	return &Breaker{
		name:     name,
		failures: cfg.Failures,
		cooldown: cfg.Cooldown,
		probes:   cfg.Probes,
		clock:    clk,
	}, nil
}

func (b *Breaker) Allow() error {
	if b == nil {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == StateOpen {
		if b.clock.Now().Sub(b.openedAt) < b.cooldown {
			metrics.ObserveBreakerRejection(b.name)

			return fmt.Errorf("%w: %s", ErrOpen, b.name)
		}

		b.enter(StateHalfOpen)
	}

	if b.state == StateHalfOpen && b.inFlight >= b.probes {
		metrics.ObserveBreakerRejection(b.name)

		return fmt.Errorf("%w: %s", ErrOpen, b.name)
	}

	b.inFlight++

	return nil
}

func (b *Breaker) Record(failed bool) {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.inFlight > 0 {
		b.inFlight--
	}

	if !failed {
		b.strikes = 0

		if b.state != StateClosed {
			b.enter(StateClosed)
		}

		return
	}

	b.strikes++

	if b.state == StateHalfOpen || b.strikes >= b.failures {
		b.enter(StateOpen)
	}
}

func (b *Breaker) Do(call func() error, failed func(error) bool) error {
	if err := b.Allow(); err != nil {
		return err
	}

	err := call()
	b.Record(failed(err))

	return err
}

func (b *Breaker) State() State {
	if b == nil {
		return StateClosed
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	return b.state
}

func (b *Breaker) enter(next State) {
	b.state = next
	b.inFlight = 0

	switch next {
	case StateClosed:
		b.strikes = 0
	case StateOpen:
		b.openedAt = b.clock.Now()
	case StateHalfOpen:
	}

	metrics.SetBreakerState(b.name, float64(next))
}
