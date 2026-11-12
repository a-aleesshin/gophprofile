package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/a-aleesshin/gophprofile/internal/outbox"
)

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

type Handler struct {
	run           func(ctx context.Context, message outbox.Envelope) error
	transactional bool
}

func (h Handler) Run(ctx context.Context, message outbox.Envelope) error {
	return h.run(ctx, message)
}

func Repeatable(run func(ctx context.Context, message outbox.Envelope) error) Handler {
	return Handler{run: run}
}

func Transactional(run func(ctx context.Context, message outbox.Envelope) error) Handler {
	return Handler{run: run, transactional: true}
}

type Stats struct {
	Handled   int
	Duplicate int
	Retried   int
	Dropped   int
}

type Consumer struct {
	name     string
	source   Source
	inbox    *Inbox
	handlers map[string]Handler
}

func NewConsumer(
	name string,
	source Source,
	in *Inbox,
	handlers map[string]Handler,
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

	return &Consumer{name: name, source: source, inbox: in, handlers: routes}, nil
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

		effect := func(ctx context.Context) error { return handler.run(ctx, envelope) }

		var (
			applied bool
			err     error
		)

		if handler.transactional {
			applied, err = c.inbox.Once(ctx, c.name, envelope.MessageID, effect)
		} else {
			applied, err = c.inbox.OnceOutsideTransaction(
				ctx, c.name, envelope.MessageID, effect)
		}

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
		if _, err := c.Consume(ctx); err != nil {
			return err
		}

		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
