package pgtx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidConfig = errors.New("invalid postgres config")

type Config struct {
	DSN             string        `env:"DSN,required,notEmpty"`
	MaxConns        int32         `env:"MAX_CONNS" envDefault:"10"`
	MinConns        int32         `env:"MIN_CONNS" envDefault:"2"`
	MaxConnLifetime time.Duration `env:"MAX_CONN_LIFETIME" envDefault:"1h"`
	MaxConnIdleTime time.Duration `env:"MAX_CONN_IDLE_TIME" envDefault:"30m"`
	ConnectTimeout  time.Duration `env:"CONNECT_TIMEOUT" envDefault:"5s"`
}

func (c Config) validate() error {
	switch {
	case c.DSN == "":
		return fmt.Errorf("%w: dsn is required", ErrInvalidConfig)
	case c.MaxConns <= 0:
		return fmt.Errorf("%w: max conns must be positive", ErrInvalidConfig)
	case c.MinConns < 0:
		return fmt.Errorf("%w: min conns must not be negative", ErrInvalidConfig)
	case c.MinConns > c.MaxConns:
		return fmt.Errorf("%w: min conns %d exceeds max conns %d",
			ErrInvalidConfig, c.MinConns, c.MaxConns)
	case c.MaxConnLifetime <= 0:
		return fmt.Errorf("%w: max conn lifetime must be positive", ErrInvalidConfig)
	case c.MaxConnIdleTime <= 0:
		return fmt.Errorf("%w: max conn idle time must be positive", ErrInvalidConfig)
	case c.ConnectTimeout <= 0:
		return fmt.Errorf("%w: connect timeout must be positive", ErrInvalidConfig)
	}

	return nil
}

func Connect(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("new pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("ping: %w", err)
	}

	return pool, nil
}
