package rabbitmq

import (
	"context"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/a-aleesshin/gophprofile/internal/outbox"
)

type Publisher struct {
	channel  *amqp.Channel
	returns  <-chan amqp.Return
	exchange string
	mu       sync.Mutex
}

func NewPublisher(conn *amqp.Connection, cfg Config) (*Publisher, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	if conn == nil {
		return nil, fmt.Errorf("%w: connection is required", ErrInvalidConfig)
	}

	channel, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("open publish channel: %w", err)
	}

	if err := channel.Confirm(false); err != nil {
		_ = channel.Close()

		return nil, fmt.Errorf("switch channel to confirm mode: %w", err)
	}

	return &Publisher{
		channel:  channel,
		returns:  channel.NotifyReturn(make(chan amqp.Return, 1)),
		exchange: cfg.Exchange,
	}, nil
}

func (p *Publisher) Send(ctx context.Context, message outbox.Pending) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	select {
	case <-p.returns:
	default:
	}

	confirmation, err := p.channel.PublishWithDeferredConfirmWithContext(
		ctx, p.exchange, message.Destination, true, false, amqp.Publishing{
			ContentType:  contentTypeJSON,
			DeliveryMode: amqp.Persistent,
			Body:         message.Payload,
			Headers:      amqp.Table{headerAttempt: int32(0)},
		})
	if err != nil {
		return fmt.Errorf("publish %s: %w", message.Destination, err)
	}

	acked, err := confirmation.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("wait for confirm of %s: %w", message.Destination, err)
	}

	if !acked {
		return fmt.Errorf("broker refused %s", message.Destination)
	}

	select {
	case returned := <-p.returns:
		return fmt.Errorf("%w: %s (%d %s)",
			ErrUnroutable, returned.RoutingKey, returned.ReplyCode, returned.ReplyText)
	default:
	}

	return nil
}

func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.channel.Close()
}
