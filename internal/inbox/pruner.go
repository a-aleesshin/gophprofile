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
	Retention time.Duration `env:"INBOX_RETENTION" envDefault:"336h"`
	BatchSize int           `env:"BATCH_SIZE" envDefault:"1000"`
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
	case cfg.BatchSize <= 0:
		return nil, fmt.Errorf("%w: batch size must be positive", ErrInvalidPrunerConfig)
	}

	return &Pruner{db: db, clock: clock, cfg: cfg}, nil
}

func (p *Pruner) Prune(ctx context.Context) (int64, error) {
	before := p.clock.Now().Add(-p.cfg.Retention)

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
