package outbox

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/a-aleesshin/gophprofile/internal/metrics"
)

const settleTimeout = 10 * time.Second

type claimed struct {
	Pending
	availableAt time.Time
	createdAt   time.Time
}

type failure struct {
	message claimed
	err     error
}

func (r *Relay) Tick(ctx context.Context) (RelayStats, error) {
	ctx, tick := r.tracer.Start(ctx, "outbox tick "+r.transport,
		trace.WithAttributes(messagingSystem(r.transport)))

	defer tick.End()

	stats, err := r.tick(ctx)

	if tick.IsRecording() {
		tick.SetAttributes(
			semconv.MessagingBatchMessageCount(stats.Published+stats.Failed),
			attribute.Int("messaging.published", stats.Published),
			attribute.Int("messaging.failed", stats.Failed),
			attribute.Int("messaging.parked", stats.Parked),
		)

		if err != nil {
			tick.RecordError(err)
			tick.SetStatus(codes.Error, err.Error())
		}
	}

	return stats, err
}

func (r *Relay) tick(ctx context.Context) (RelayStats, error) {
	now := r.clock.Now()

	leased, err := r.lease(ctx, now)
	if err != nil {
		return RelayStats{}, err
	}

	if len(leased) == 0 {
		return RelayStats{}, nil
	}

	publishCtx, span := r.tracer.Start(ctx, "outbox publish "+r.transport,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			messagingSystem(r.transport),
			semconv.MessagingOperationTypeSend,
			semconv.MessagingBatchMessageCount(len(leased)),
		))

	published, failures, sendErr := r.publish(publishCtx, leased)

	if span.IsRecording() {
		span.SetAttributes(
			attribute.Int("messaging.published", len(published)),
			attribute.Int("messaging.failed", len(failures)),
		)

		if sendErr != nil {
			span.RecordError(sendErr)
			span.SetStatus(codes.Error, sendErr.Error())
		}
	}

	span.End()

	untouched := untouchedOf(leased, published, failures)

	settleCtx := ctx

	if sendErr != nil {
		detached, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), settleTimeout)
		defer cancel()

		settleCtx = detached
	}

	if err := r.settle(settleCtx, now, published, failures, untouched); err != nil {
		return RelayStats{}, err
	}

	if sendErr != nil {
		return RelayStats{}, sendErr
	}

	var parked []ParkedMessage

	for _, item := range failures {
		if r.giveUpOn(item, now) {
			parked = append(parked, ParkedMessage{
				ID:           item.message.ID,
				Destination:  item.message.Destination,
				PartitionKey: item.message.PartitionKey,
				Type:         item.message.Type,
				Attempts:     item.message.Attempts + 1,
				Err:          item.err,
			})
		}
	}

	return RelayStats{
		Published:  len(published),
		Failed:     len(failures),
		Parked:     len(parked),
		ParkedRows: parked,
	}, nil
}

func (r *Relay) lease(ctx context.Context, now time.Time) ([]claimed, error) {
	var eligible []claimed

	err := r.db.InTx(ctx, func(ctx context.Context) error {
		rows, err := r.db.Querier(ctx).Query(ctx, `
			SELECT o.id, o.destination, o.partition_key, o.message_type, o.payload,
				o.attempts, o.available_at, o.created_at
			  FROM outbox AS o
			 WHERE o.transport = $1
			   AND o.published_at IS NULL
			   AND o.failed_at IS NULL
			   AND o.available_at <= $2
			   AND (o.partition_key IS NULL OR NOT EXISTS (
					SELECT 1
					  FROM outbox AS ahead
					 WHERE ahead.transport = o.transport
					   AND ahead.partition_key = o.partition_key
					   AND ahead.published_at IS NULL
					   AND ahead.failed_at IS NULL
					   AND ahead.id < o.id
					   AND ahead.available_at > $2))
			 ORDER BY o.id
			 LIMIT $3
			   FOR UPDATE SKIP LOCKED`, r.transport, now, r.cfg.BatchSize)
		if err != nil {
			return fmt.Errorf("select outbox batch: %w", err)
		}

		eligible, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (claimed, error) {
			var (
				message claimed
				key     *string
			)

			if err := row.Scan(&message.ID, &message.Destination, &key, &message.Type,
				&message.Payload, &message.Attempts, &message.availableAt,
				&message.createdAt); err != nil {
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

		if len(eligible) == 0 {
			return nil
		}

		ids := make([]int64, 0, len(eligible))
		for _, message := range eligible {
			ids = append(ids, message.ID)
		}

		if _, err := r.db.Querier(ctx).Exec(ctx, `
			UPDATE outbox SET available_at = $2 WHERE id = ANY($1)`,
			ids, now.Add(r.cfg.Lease)); err != nil {
			return fmt.Errorf("lease outbox batch: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return eligible, nil
}

func (r *Relay) publish(ctx context.Context, leased []claimed) ([]int64, []failure, error) {
	published := make([]int64, 0, len(leased))
	failures := make([]failure, 0, len(leased))
	blocked := make(map[string]struct{}, len(leased))
	consecutive := 0

	for _, message := range leased {
		if consecutive >= r.cfg.MaxConsecutiveFailures {
			break
		}

		scope := scopeOf(message)

		if _, stop := blocked[scope]; stop {
			continue
		}

		if sendErr := r.sender.Send(ctx, message.Pending); sendErr != nil {
			if ctx.Err() != nil {
				return published, failures, errors.Join(sendErr, ctx.Err())
			}

			blocked[scope] = struct{}{}
			failures = append(failures, failure{message: message, err: sendErr})
			consecutive++

			continue
		}

		consecutive = 0
		published = append(published, message.ID)
	}

	return published, failures, nil
}

func untouchedOf(leased []claimed, published []int64, failures []failure) []claimed {
	settled := make(map[int64]struct{}, len(published)+len(failures))

	for _, id := range published {
		settled[id] = struct{}{}
	}

	for _, item := range failures {
		settled[item.message.ID] = struct{}{}
	}

	untouched := make([]claimed, 0, len(leased))

	for _, message := range leased {
		if _, done := settled[message.ID]; !done {
			untouched = append(untouched, message)
		}
	}

	return untouched
}

func (r *Relay) settle(
	ctx context.Context,
	now time.Time,
	published []int64,
	failures []failure,
	untouched []claimed,
) error {
	if len(published) == 0 && len(failures) == 0 && len(untouched) == 0 {
		return nil
	}

	return r.db.InTx(ctx, func(ctx context.Context) error {
		if len(untouched) > 0 {
			ids := make([]int64, 0, len(untouched))
			schedule := make([]time.Time, 0, len(untouched))

			for _, message := range untouched {
				ids = append(ids, message.ID)
				schedule = append(schedule, message.availableAt)
			}

			if _, err := r.db.Querier(ctx).Exec(ctx, `
				UPDATE outbox AS o
				   SET available_at = f.available_at
				  FROM unnest($1::bigint[], $2::timestamptz[])
				    AS f(id, available_at)
				 WHERE o.id = f.id`, ids, schedule); err != nil {
				return fmt.Errorf("release unsent outbox rows: %w", err)
			}
		}

		if len(published) > 0 {
			if _, err := r.db.Querier(ctx).Exec(ctx, `
				UPDATE outbox
				   SET published_at = $2, attempts = attempts + 1
				 WHERE id = ANY($1)`, published, now); err != nil {
				return fmt.Errorf("mark outbox published: %w", err)
			}
		}

		if len(failures) == 0 {
			return nil
		}

		ids := make([]int64, 0, len(failures))
		reasons := make([]string, 0, len(failures))
		retryAt := make([]time.Time, 0, len(failures))

		parkedIDs := make([]int64, 0, len(failures))
		parkedReasons := make([]string, 0, len(failures))

		for _, item := range failures {
			if r.giveUpOn(item, now) {
				parkedIDs = append(parkedIDs, item.message.ID)
				parkedReasons = append(parkedReasons, item.err.Error())

				continue
			}

			ids = append(ids, item.message.ID)
			reasons = append(reasons, item.err.Error())
			retryAt = append(retryAt, now.Add(backoffFor(
				item.message.Attempts, r.cfg.MinBackoff, r.cfg.MaxBackoff)))
		}

		if len(ids) > 0 {
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

		if len(parkedIDs) == 0 {
			return nil
		}

		if _, err := r.db.Querier(ctx).Exec(ctx, `
			UPDATE outbox AS o
			   SET attempts = o.attempts + 1,
			       last_error = f.last_error,
			       failed_at = $3
			  FROM unnest($1::bigint[], $2::text[])
			    AS f(id, last_error)
			 WHERE o.id = f.id`, parkedIDs, parkedReasons, now); err != nil {
			return fmt.Errorf("park outbox rows: %w", err)
		}

		return nil
	})
}

func (r *Relay) giveUpOn(item failure, now time.Time) bool {
	if errors.Is(item.err, ErrUnpublishable) {
		return true
	}

	return now.Sub(item.message.createdAt) >= r.cfg.MaxAge
}

func (r *Relay) Run(ctx context.Context) error {
	timer := time.NewTimer(r.cfg.Interval)

	defer timer.Stop()

	for {
		stats, err := r.Tick(ctx)

		metrics.ObserveOutboxTick(r.transport, stats.Published, stats.Failed, stats.Parked)

		for _, message := range stats.ParkedRows {
			r.logger.WarnContext(ctx, "parked an outbox row",
				"transport", r.transport,
				"outbox_id", message.ID,
				"destination", message.Destination,
				"partition_key", message.PartitionKey,
				"message_type", message.Type,
				"attempts", message.Attempts,
				"error", message.Err)
		}

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

// ObserveBacklog reports how long the oldest unpublished row has been waiting.
// It is a separate job rather than part of Tick: a relay that cannot lease
// anything never reaches the end of a tick, and that is exactly the state the
// gauge has to describe.
func (r *Relay) ObserveBacklog(ctx context.Context) error {
	var (
		oldest  *time.Time
		pending int64
		parked  int64
	)

	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT min(created_at) FILTER (WHERE published_at IS NULL AND failed_at IS NULL),
		       count(*) FILTER (WHERE published_at IS NULL AND failed_at IS NULL),
		       count(*) FILTER (WHERE failed_at IS NOT NULL)
		  FROM outbox
		 WHERE transport = $1`, r.transport).Scan(&oldest, &pending, &parked)
	if err != nil {
		return fmt.Errorf("select the %s backlog: %w", r.transport, err)
	}

	metrics.SetOutboxDepth(r.transport, pending, parked)

	if oldest == nil {
		metrics.SetOutboxBacklogAge(r.transport, 0)

		return nil
	}

	age := r.clock.Now().Sub(*oldest)
	if age < 0 {
		age = 0
	}

	metrics.SetOutboxBacklogAge(r.transport, age)

	return nil
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
