package rabbitmq

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/a-aleesshin/gophprofile/internal/inbox"
)

type Consumer struct {
	channel *amqp.Channel
	stream  <-chan amqp.Delivery
	cfg     Config
	queue   string
}

func NewConsumer(
	conn *amqp.Connection,
	cfg Config,
	routingKey string,
	tag string,
) (*Consumer, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	switch {
	case conn == nil:
		return nil, fmt.Errorf("%w: connection is required", ErrInvalidConfig)
	case routingKey == "":
		return nil, fmt.Errorf("%w: routing key is required", ErrInvalidConfig)
	case tag == "":
		return nil, fmt.Errorf("%w: consumer tag is required", ErrInvalidConfig)
	}

	channel, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("open consume channel: %w", err)
	}

	if err := channel.Qos(cfg.Prefetch, 0, false); err != nil {
		_ = channel.Close()

		return nil, fmt.Errorf("set prefetch: %w", err)
	}

	if err := channel.Confirm(false); err != nil {
		_ = channel.Close()

		return nil, fmt.Errorf("switch channel to confirm mode: %w", err)
	}

	queue := cfg.commandQueue(routingKey)

	stream, err := channel.Consume(queue, tag, false, false, false, false, nil)
	if err != nil {
		_ = channel.Close()

		return nil, fmt.Errorf("consume %s: %w", queue, err)
	}

	return &Consumer{channel: channel, stream: stream, cfg: cfg, queue: queue}, nil
}

func (c *Consumer) Fetch(ctx context.Context) ([]inbox.Delivery, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case message, open := <-c.stream:
		if !open {
			return nil, fmt.Errorf("%w: %s", ErrChannelClosed, c.queue)
		}

		return []inbox.Delivery{&delivery{message: message, channel: c.channel, cfg: c.cfg}}, nil
	}
}

func (c *Consumer) Close() error {
	return c.channel.Close()
}

type delivery struct {
	message amqp.Delivery
	channel *amqp.Channel
	cfg     Config
}

func (d *delivery) Body() []byte { return d.message.Body }

func (d *delivery) Ack(context.Context) error {
	if err := d.message.Ack(false); err != nil {
		return fmt.Errorf("ack: %w", err)
	}

	return nil
}

func (d *delivery) Retry(ctx context.Context) error {
	attempt := attemptOf(d.message.Headers)

	if attempt >= len(d.cfg.Ladder) {
		return d.deadLetter(ctx, fmt.Sprintf("gave up after %d attempts", attempt))
	}

	headers := cloneHeaders(d.message.Headers)
	headers[headerAttempt] = int32(attempt + 1)

	if err := d.republish(ctx, d.cfg.retryExchange(attempt), d.message.RoutingKey, headers); err != nil {
		return err
	}

	return d.Ack(ctx)
}

func (d *delivery) Drop(ctx context.Context, reason string) error {
	return d.deadLetter(ctx, reason)
}

func (d *delivery) deadLetter(ctx context.Context, reason string) error {
	headers := cloneHeaders(d.message.Headers)
	headers[headerDropReason] = reason
	headers[headerOriginalKey] = d.message.RoutingKey

	if err := d.republish(ctx, d.cfg.deadLetterExchange(), "", headers); err != nil {
		return err
	}

	return d.Ack(ctx)
}

func (d *delivery) republish(
	ctx context.Context,
	exchange string,
	routingKey string,
	headers amqp.Table,
) error {
	confirmation, err := d.channel.PublishWithDeferredConfirmWithContext(
		ctx, exchange, routingKey, false, false, amqp.Publishing{
			ContentType:  contentTypeJSON,
			DeliveryMode: amqp.Persistent,
			MessageId:    d.message.MessageId,
			Headers:      headers,
			Body:         d.message.Body,
		})
	if err != nil {
		return fmt.Errorf("publish to %s: %w", exchange, err)
	}

	acked, err := confirmation.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("wait for confirm from %s: %w", exchange, err)
	}

	if !acked {
		return fmt.Errorf("broker refused the message for %s", exchange)
	}

	return nil
}

func cloneHeaders(headers amqp.Table) amqp.Table {
	cloned := make(amqp.Table, len(headers)+2)

	for key, value := range headers {
		cloned[key] = value
	}

	return cloned
}
