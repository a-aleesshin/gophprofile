package pgtx

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/a-aleesshin/gophprofile/internal/metrics"
)

func ObservePool(name string, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("%w: pool is required", ErrInvalidConfig)
	}

	return metrics.RegisterPool(name, func() metrics.PoolStats {
		stat := pool.Stat()

		return metrics.PoolStats{
			Acquired:      stat.AcquiredConns(),
			Idle:          stat.IdleConns(),
			Total:         stat.TotalConns(),
			Max:           stat.MaxConns(),
			EmptyAcquires: stat.EmptyAcquireCount(),
		}
	})
}
