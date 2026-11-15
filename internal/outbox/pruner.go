package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

var ErrInvalidPrunerConfig = errors.New("invalid pruner config")

type PrunerConfig struct {
	Retention       time.Duration `env:"OUTBOX_RETENTION" envDefault:"72h"`
	ParkedRetention time.Duration `env:"OUTBOX_PARKED_RETENTION" envDefault:"720h"`
	BatchSize       int           `env:"OUTBOX_BATCH_SIZE" envDefault:"1000"`
}

type PruneResult struct {
	Published int64
	Parked    int64
}

type Pruner struct {
	db    *pgtx.DB
	clock Clock
	cfg   PrunerConfig
}

func NewPruner(db *pgtx.DB, clock Clock, cfg PrunerConfig) (*Pruner, error) {
	switch {
	case db == nil:
		return nil, fmt.Errorf("%w: db is required", ErrInvalidPrunerConfig)
	case clock == nil:
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidPrunerConfig)
	case cfg.Retention <= 0:
		return nil, fmt.Errorf("%w: retention must be positive", ErrInvalidPrunerConfig)
	case cfg.ParkedRetention <= 0:
		return nil, fmt.Errorf("%w: parked retention must be positive", ErrInvalidPrunerConfig)
	case cfg.BatchSize <= 0:
		return nil, fmt.Errorf("%w: batch size must be positive", ErrInvalidPrunerConfig)
	}

	return &Pruner{db: db, clock: clock, cfg: cfg}, nil
}

func (p *Pruner) Prune(ctx context.Context) (PruneResult, error) {
	now := p.clock.Now()

	var result PruneResult

	published, err := p.drain(ctx, p.prunePublishedBatch, now.Add(-p.cfg.Retention))
	result.Published = published

	if err != nil {
		return result, err
	}

	parked, err := p.drain(ctx, p.pruneParkedBatch, now.Add(-p.cfg.ParkedRetention))
	result.Parked = parked

	return result, err
}

func (p *Pruner) drain(
	ctx context.Context,
	batch func(context.Context, time.Time) (int64, error),
	before time.Time,
) (int64, error) {
	var total int64

	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}

		removed, err := batch(ctx, before)
		if err != nil {
			return total, err
		}

		total += removed

		if removed < int64(p.cfg.BatchSize) {
			return total, nil
		}
	}
}

func (p *Pruner) prunePublishedBatch(ctx context.Context, before time.Time) (int64, error) {
	tag, err := p.db.Querier(ctx).Exec(ctx, `
		DELETE FROM outbox
		 WHERE id IN (
			SELECT id
			  FROM outbox
			 WHERE published_at IS NOT NULL AND published_at < $1
			 ORDER BY published_at
			 LIMIT $2
		 )`, before, p.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("prune outbox published before %s: %w", before, err)
	}

	return tag.RowsAffected(), nil
}

func (p *Pruner) pruneParkedBatch(ctx context.Context, before time.Time) (int64, error) {
	tag, err := p.db.Querier(ctx).Exec(ctx, `
		DELETE FROM outbox
		 WHERE id IN (
			SELECT id
			  FROM outbox
			 WHERE failed_at IS NOT NULL AND failed_at < $1
			 ORDER BY failed_at
			 LIMIT $2
		 )`, before, p.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("prune outbox parked before %s: %w", before, err)
	}

	return tag.RowsAffected(), nil
}
