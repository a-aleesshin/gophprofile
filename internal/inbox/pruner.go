package inbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

var ErrInvalidPrunerConfig = errors.New("invalid pruner config")

type Clock interface {
	Now() time.Time
}

type PrunerConfig struct {
	Retention        time.Duration `env:"INBOX_RETENTION" envDefault:"336h"`
	DroppedRetention time.Duration `env:"INBOX_DROPPED_RETENTION" envDefault:"720h"`
	BatchSize        int           `env:"INBOX_BATCH_SIZE" envDefault:"1000"`
}

type PruneResult struct {
	Processed int64
	Failures  int64
	Dropped   int64
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
	case cfg.DroppedRetention <= 0:
		return nil, fmt.Errorf("%w: dropped retention must be positive", ErrInvalidPrunerConfig)
	case cfg.BatchSize <= 0:
		return nil, fmt.Errorf("%w: batch size must be positive", ErrInvalidPrunerConfig)
	}

	return &Pruner{db: db, clock: clock, cfg: cfg}, nil
}

func (p *Pruner) Prune(ctx context.Context) (PruneResult, error) {
	now := p.clock.Now()
	stale := now.Add(-p.cfg.Retention)

	var result PruneResult

	processed, err := p.drain(ctx, p.pruneProcessedBatch, stale)
	result.Processed = processed

	if err != nil {
		return result, err
	}

	failures, err := p.drain(ctx, p.pruneOpenFailureBatch, stale)
	result.Failures = failures

	if err != nil {
		return result, err
	}

	dropped, err := p.drain(ctx, p.pruneDroppedBatch, now.Add(-p.cfg.DroppedRetention))
	result.Dropped = dropped

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

func (p *Pruner) pruneOpenFailureBatch(
	ctx context.Context,
	before time.Time,
) (int64, error) {
	tag, err := p.db.Querier(ctx).Exec(ctx, `
		DELETE FROM failed_messages
		 WHERE (consumer, message_id) IN (
			SELECT consumer, message_id
			  FROM failed_messages
			 WHERE dropped_at IS NULL AND last_failed_at < $1
			 ORDER BY last_failed_at
			 LIMIT $2
		 )`, before, p.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("prune open inbox failures before %s: %w", before, err)
	}

	return tag.RowsAffected(), nil
}

func (p *Pruner) pruneDroppedBatch(ctx context.Context, before time.Time) (int64, error) {
	tag, err := p.db.Querier(ctx).Exec(ctx, `
		DELETE FROM failed_messages
		 WHERE (consumer, message_id) IN (
			SELECT consumer, message_id
			  FROM failed_messages
			 WHERE dropped_at IS NOT NULL AND dropped_at < $1
			 ORDER BY dropped_at
			 LIMIT $2
		 )`, before, p.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("prune dropped inbox messages before %s: %w", before, err)
	}

	return tag.RowsAffected(), nil
}

func (p *Pruner) pruneProcessedBatch(ctx context.Context, before time.Time) (int64, error) {
	tag, err := p.db.Querier(ctx).Exec(ctx, `
		DELETE FROM processed_messages
		 WHERE (consumer, message_id) IN (
			SELECT consumer, message_id
			  FROM processed_messages
			 WHERE processed_at < $1
			 ORDER BY processed_at
			 LIMIT $2
		 )`, before, p.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("prune processed messages before %s: %w", before, err)
	}

	return tag.RowsAffected(), nil
}
