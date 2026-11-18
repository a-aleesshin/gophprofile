package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/a-aleesshin/gophprofile/internal/metrics"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
)

const tracerName = "github.com/a-aleesshin/gophprofile/internal/inbox"

var (
	ErrInvalidConsumer = errors.New("invalid consumer")
	ErrUnprocessable   = errors.New("message can never be processed")
)

type Delivery interface {
	Body() []byte
	Ack(ctx context.Context) error
	Retry(ctx context.Context) error
	Drop(ctx context.Context, reason string) error
}

type Source interface {
	Fetch(ctx context.Context) ([]Delivery, error)
}

type Run func(ctx context.Context, message outbox.Envelope) error

type RunSettling func(ctx context.Context, message outbox.Envelope) (func(), error)

type Handler struct {
	run           RunSettling
	transactional bool
}

func (h Handler) Run(ctx context.Context, message outbox.Envelope) error {
	_, err := h.run(ctx, message)

	return err
}

func Repeatable(run Run) Handler {
	return Handler{run: settlingNothing(run)}
}

func Transactional(run Run) Handler {
	return Handler{run: settlingNothing(run), transactional: true}
}

func TransactionalSettling(run RunSettling) Handler {
	return Handler{run: run, transactional: true}
}

func settlingNothing(run Run) RunSettling {
	return func(ctx context.Context, message outbox.Envelope) (func(), error) {
		return nil, run(ctx, message)
	}
}

type ConsumerConfig struct {
	MaxAttempts int `env:"MAX_ATTEMPTS" envDefault:"10"`
}

type Stats struct {
	Handled   int
	Duplicate int
	Retried   int
	Dropped   int
	Exhausted int
}

type Consumer struct {
	name        string
	source      Source
	inbox       *Inbox
	handlers    map[string]Handler
	tracer      trace.Tracer
	maxAttempts int
}

func NewConsumer(
	name string,
	source Source,
	in *Inbox,
	handlers map[string]Handler,
	maxAttempts int,
) (*Consumer, error) {
	switch {
	case name == "":
		return nil, fmt.Errorf("%w: name is required", ErrInvalidConsumer)
	case len(name) > 64:
		return nil, fmt.Errorf("%w: name must fit processed_messages.consumer", ErrInvalidConsumer)
	case source == nil:
		return nil, fmt.Errorf("%w: source is required", ErrInvalidConsumer)
	case in == nil:
		return nil, fmt.Errorf("%w: inbox is required", ErrInvalidConsumer)
	case len(handlers) == 0:
		return nil, fmt.Errorf("%w: at least one handler is required", ErrInvalidConsumer)
	case maxAttempts <= 0:
		return nil, fmt.Errorf(
			"%w: max attempts must be positive, or a message nothing can apply blocks its partition forever",
			ErrInvalidConsumer)
	}

	routes := make(map[string]Handler, len(handlers))

	for messageType, handler := range handlers {
		if messageType == "" {
			return nil, fmt.Errorf("%w: a handler needs a message type", ErrInvalidConsumer)
		}

		if handler.run == nil {
			return nil, fmt.Errorf("%w: handler for %q has nothing to run",
				ErrInvalidConsumer, messageType)
		}

		routes[messageType] = handler
	}

	metrics.InitInbox(name)

	return &Consumer{
		name:        name,
		source:      source,
		inbox:       in,
		handlers:    routes,
		tracer:      otel.Tracer(tracerName),
		maxAttempts: maxAttempts,
	}, nil
}

func (c *Consumer) Consume(ctx context.Context) (Stats, error) {
	var stats Stats

	deliveries, err := c.source.Fetch(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("fetch for %s: %w", c.name, err)
	}

	for _, delivery := range deliveries {
		var envelope outbox.Envelope

		if err := json.Unmarshal(delivery.Body(), &envelope); err != nil {
			if dropErr := delivery.Drop(ctx, "malformed envelope"); dropErr != nil {
				return stats, fmt.Errorf("drop malformed message: %w", dropErr)
			}

			stats.Dropped++

			continue
		}

		handler, known := c.handlers[envelope.Type]

		if !known || envelope.MessageID == uuid.Nil {
			reason := fmt.Sprintf("no handler for %q", envelope.Type)
			if envelope.MessageID == uuid.Nil {
				reason = "envelope carries no message id"
			}

			if dropErr := delivery.Drop(ctx, reason); dropErr != nil {
				return stats, fmt.Errorf("drop message: %w", dropErr)
			}

			stats.Dropped++

			continue
		}

		var settled func()

		effect := func(ctx context.Context) error {
			var runErr error

			settled, runErr = handler.run(ctx, envelope)

			return runErr
		}

		var (
			applied bool
			err     error
		)

		handled, span := c.tracer.Start(envelope.Context(ctx), "process "+envelope.Type,
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				semconv.MessagingOperationTypeProcess,
				semconv.MessagingDestinationName(c.name),
				semconv.MessagingMessageID(envelope.MessageID.String()),
			))

		if handler.transactional {
			applied, err = c.inbox.Once(handled, c.name, envelope.MessageID, effect)
		} else {
			applied, err = c.inbox.OnceOutsideTransaction(
				handled, c.name, envelope.MessageID, effect)
		}

		if applied && err == nil && settled != nil {
			settled()
		}

		if span.IsRecording() {
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}

			span.SetAttributes(attribute.Bool("inbox.applied", applied))
		}

		span.End()

		if err != nil {
			if ctx.Err() != nil {
				return stats, errors.Join(err, ctx.Err())
			}

			if errors.Is(err, ErrUnprocessable) {
				if dropErr := delivery.Drop(ctx, err.Error()); dropErr != nil {
					return stats, fmt.Errorf("drop message: %w", dropErr)
				}

				stats.Dropped++

				continue
			}

			failure, recordErr := c.inbox.RecordFailure(
				ctx, c.name, envelope.MessageID, err, c.maxAttempts)
			if recordErr != nil {
				return stats, errors.Join(err, recordErr)
			}

			if failure.Dropped {
				reason := fmt.Sprintf("gave up after %d attempts: %s", failure.Attempts, err)

				if dropErr := delivery.Drop(ctx, reason); dropErr != nil {
					return stats, fmt.Errorf("drop exhausted message: %w", dropErr)
				}

				stats.Exhausted++

				continue
			}

			if retryErr := delivery.Retry(ctx); retryErr != nil {
				return stats, fmt.Errorf("retry message: %w", retryErr)
			}

			stats.Retried++

			continue
		}

		if err := delivery.Ack(ctx); err != nil {
			return stats, fmt.Errorf("ack message: %w", err)
		}

		if applied {
			stats.Handled++
		} else {
			stats.Duplicate++
		}
	}

	return stats, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	for {
		stats, err := c.Consume(ctx)

		metrics.ObserveInbox(c.name,
			stats.Handled, stats.Duplicate, stats.Retried, stats.Dropped, stats.Exhausted)

		if err != nil {
			return err
		}

		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
