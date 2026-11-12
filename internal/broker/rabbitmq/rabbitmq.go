package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	ErrInvalidConfig = errors.New("invalid rabbitmq config")
	ErrChannelClosed = errors.New("rabbitmq channel is closed")
	ErrUnroutable    = errors.New("rabbitmq returned the message as unroutable")
	ErrInTransaction = errors.New("a broker call must not run inside a database transaction")
)

const (
	headerAttempt     = "x-attempt"
	headerDropReason  = "x-drop-reason"
	headerOriginalKey = "x-original-routing-key"
	contentTypeJSON   = "application/json"
)

type Config struct {
	URL      string          `env:"URL,required,notEmpty"`
	Exchange string          `env:"EXCHANGE" envDefault:"gophprofile.commands"`
	Prefetch int             `env:"PREFETCH" envDefault:"3"`
	Ladder   []time.Duration `env:"RETRY_LADDER" envDefault:"1s,4s,16s,64s,256s" envSeparator:","`
}

func (c Config) validate() error {
	switch {
	case c.URL == "":
		return fmt.Errorf("%w: url is required", ErrInvalidConfig)
	case c.Exchange == "":
		return fmt.Errorf("%w: exchange is required", ErrInvalidConfig)
	case c.Prefetch <= 0:
		return fmt.Errorf("%w: prefetch must be positive", ErrInvalidConfig)
	case len(c.Ladder) == 0:
		return fmt.Errorf("%w: the retry ladder must have at least one step", ErrInvalidConfig)
	}

	for i, step := range c.Ladder {
		if step <= 0 {
			return fmt.Errorf("%w: retry step %d must be positive", ErrInvalidConfig, i)
		}

		if i > 0 && step <= c.Ladder[i-1] {
			return fmt.Errorf("%w: the retry ladder must grow, step %d is not above step %d",
				ErrInvalidConfig, i, i-1)
		}
	}

	return nil
}

func (c Config) deadLetterExchange() string { return c.Exchange + ".dlx" }

func (c Config) deadLetterQueue() string { return c.Exchange + ".dlq" }

func (c Config) retryExchange(step int) string {
	return c.Exchange + ".retry." + strconv.Itoa(step)
}

func (c Config) retryQueue(step int) string {
	return c.Exchange + ".retry." + strconv.Itoa(step)
}

func (c Config) commandQueue(routingKey string) string {
	return c.Exchange + "." + routingKey
}

func Declare(ctx context.Context, conn *amqp.Connection, cfg Config, routingKeys []string) error {
	if err := cfg.validate(); err != nil {
		return err
	}

	if len(routingKeys) == 0 {
		return fmt.Errorf("%w: at least one routing key is required", ErrInvalidConfig)
	}

	channel, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}

	defer func() { _ = channel.Close() }()

	if err := channel.ExchangeDeclare(
		cfg.Exchange, amqp.ExchangeDirect, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", cfg.Exchange, err)
	}

	if err := channel.ExchangeDeclare(
		cfg.deadLetterExchange(), amqp.ExchangeFanout, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", cfg.deadLetterExchange(), err)
	}

	if _, err := channel.QueueDeclare(cfg.deadLetterQueue(), true, false, false, false,
		amqp.Table{"x-queue-type": "quorum"}); err != nil {
		return fmt.Errorf("declare queue %s: %w", cfg.deadLetterQueue(), err)
	}

	if err := channel.QueueBind(
		cfg.deadLetterQueue(), "", cfg.deadLetterExchange(), false, nil); err != nil {
		return fmt.Errorf("bind queue %s: %w", cfg.deadLetterQueue(), err)
	}

	for _, routingKey := range routingKeys {
		queue := cfg.commandQueue(routingKey)

		if _, err := channel.QueueDeclare(queue, true, false, false, false, amqp.Table{
			"x-queue-type":           "quorum",
			"x-dead-letter-exchange": cfg.deadLetterExchange(),
		}); err != nil {
			return fmt.Errorf("declare queue %s: %w", queue, err)
		}

		if err := channel.QueueBind(queue, routingKey, cfg.Exchange, false, nil); err != nil {
			return fmt.Errorf("bind queue %s: %w", queue, err)
		}
	}

	for step, delay := range cfg.Ladder {
		exchange := cfg.retryExchange(step)
		queue := cfg.retryQueue(step)

		if err := channel.ExchangeDeclare(
			exchange, amqp.ExchangeFanout, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare exchange %s: %w", exchange, err)
		}

		if _, err := channel.QueueDeclare(queue, true, false, false, false, amqp.Table{
			"x-message-ttl":          delay.Milliseconds(),
			"x-dead-letter-exchange": cfg.Exchange,
		}); err != nil {
			return fmt.Errorf("declare queue %s: %w", queue, err)
		}

		if err := channel.QueueBind(queue, "", exchange, false, nil); err != nil {
			return fmt.Errorf("bind queue %s: %w", queue, err)
		}
	}

	return nil
}

func attemptOf(headers amqp.Table) int {
	raw, ok := headers[headerAttempt]
	if !ok {
		return 0
	}

	switch value := raw.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	default:
		return 0
	}
}
