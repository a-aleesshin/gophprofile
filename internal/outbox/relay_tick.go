package outbox

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

type claimed struct {
	Pending
	availableAt time.Time
}

type failure struct {
	message claimed
	err     error
}

func (r *Relay) Tick(ctx context.Context) (RelayStats, error) {
	var stats RelayStats

	err := r.db.InTx(ctx, func(ctx context.Context) error {
		now := r.clock.Now()

		rows, err := r.db.Querier(ctx).Query(ctx, `
			SELECT id, destination, partition_key, message_type, payload,
				attempts, available_at
			  FROM outbox
			 WHERE transport = $1 AND published_at IS NULL
			 ORDER BY id
			 LIMIT $2
			   FOR UPDATE SKIP LOCKED`, r.transport, r.cfg.BatchSize)
		if err != nil {
			return fmt.Errorf("select outbox batch: %w", err)
		}

		pending, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (claimed, error) {
			var (
				message claimed
				key     *string
			)

			if err := row.Scan(&message.ID, &message.Destination, &key, &message.Type,
				&message.Payload, &message.Attempts, &message.availableAt); err != nil {
				return claimed{}, err
			}

			if key != nil {
				message.PartitionKey = *key
			}

			message.Transport = r.transport

			return message, nil
		})
		if err != nil {
			return fmt.Errorf("read outbox batch: %w", err)
		}

		if len(pending) == 0 {
			return nil
		}

		published := make([]int64, 0, len(pending))
		failures := make([]failure, 0, len(pending))
		blocked := make(map[string]struct{}, len(pending))
		consecutive := 0

		for _, message := range pending {
			if consecutive >= r.cfg.MaxConsecutiveFailures {
				break
			}

			scope := scopeOf(message)

			if _, stop := blocked[scope]; stop {
				continue
			}

			if message.availableAt.After(now) {
				blocked[scope] = struct{}{}

				continue
			}

			if sendErr := r.sender.Send(ctx, message.Pending); sendErr != nil {
				if ctx.Err() != nil {
					return errors.Join(sendErr, ctx.Err())
				}

				blocked[scope] = struct{}{}
				failures = append(failures, failure{message: message, err: sendErr})
				consecutive++

				continue
			}

			consecutive = 0
			published = append(published, message.ID)
		}

		if len(published) > 0 {
			if _, err := r.db.Querier(ctx).Exec(ctx, `
				UPDATE outbox
				   SET published_at = $2, attempts = attempts + 1
				 WHERE id = ANY($1)`, published, now); err != nil {
				return fmt.Errorf("mark outbox published: %w", err)
			}
		}

		if len(failures) > 0 {
			ids := make([]int64, 0, len(failures))
			reasons := make([]string, 0, len(failures))
			retryAt := make([]time.Time, 0, len(failures))

			for _, item := range failures {
				ids = append(ids, item.message.ID)
				reasons = append(reasons, item.err.Error())
				retryAt = append(retryAt, now.Add(backoffFor(
					item.message.Attempts, r.cfg.MinBackoff, r.cfg.MaxBackoff)))
			}

			if _, err := r.db.Querier(ctx).Exec(ctx, `
				UPDATE outbox AS o
				   SET attempts = o.attempts + 1,
				       last_error = f.last_error,
				       available_at = f.available_at
				  FROM unnest($1::bigint[], $2::text[], $3::timestamptz[])
				    AS f(id, last_error, available_at)
				 WHERE o.id = f.id`, ids, reasons, retryAt); err != nil {
				return fmt.Errorf("mark outbox failed: %w", err)
			}
		}

		stats.Published = len(published)
		stats.Failed = len(failures)

		return nil
	})
	if err != nil {
		return RelayStats{}, err
	}

	return stats, nil
}

func (r *Relay) Run(ctx context.Context) error {
	timer := time.NewTimer(r.cfg.Interval)

	defer timer.Stop()

	for {
		stats, err := r.Tick(ctx)
		if err != nil {
			return err
		}

		if stats.Published == r.cfg.BatchSize {
			if err := ctx.Err(); err != nil {
				return err
			}

			continue
		}

		timer.Reset(r.cfg.Interval)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func scopeOf(message claimed) string {
	if message.PartitionKey == "" {
		return "id:" + strconv.FormatInt(message.ID, 10)
	}

	return "key:" + message.PartitionKey
}

func backoffFor(attempts int, minDelay, maxDelay time.Duration) time.Duration {
	delay := minDelay

	for range attempts {
		if delay >= maxDelay/2 {
			return maxDelay
		}

		delay *= 2
	}

	if delay > maxDelay {
		return maxDelay
	}

	return delay
}
