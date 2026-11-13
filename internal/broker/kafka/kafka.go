package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
)

var (
	ErrInvalidConfig  = errors.New("invalid kafka config")
	ErrNoPartitionKey = errors.New("a fact without a partition key would lose its order")
	ErrInTransaction  = errors.New("a broker call must not run inside a database transaction")
)

type Config struct {
	Brokers      []string      `env:"BROKERS,required,notEmpty" envSeparator:","`
	Topic        string        `env:"TOPIC" envDefault:"gophprofile.file.v1"`
	Group        string        `env:"GROUP"`
	RetryBackoff time.Duration `env:"RETRY_BACKOFF" envDefault:"1s"`
}

type TransactionGuard interface {
	InTransaction(ctx context.Context) bool
}

type Producer struct {
	client *kgo.Client
	guard  TransactionGuard
}

func NewProducer(cfg Config, guard TransactionGuard) (*Producer, error) {
	switch {
	case len(cfg.Brokers) == 0:
		return nil, fmt.Errorf("%w: at least one broker is required", ErrInvalidConfig)
	case guard == nil:
		return nil, fmt.Errorf("%w: transaction guard is required", ErrInvalidConfig)
	}

	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerLinger(0),
	)
	if err != nil {
		return nil, fmt.Errorf("open kafka producer: %w", err)
	}

	return &Producer{client: client, guard: guard}, nil
}

func (p *Producer) Send(ctx context.Context, message outbox.Pending) error {
	if p.guard.InTransaction(ctx) {
		return fmt.Errorf("%w: producing %s", ErrInTransaction, message.Type)
	}

	if message.PartitionKey == "" {
		return fmt.Errorf("%w: %s", ErrNoPartitionKey, message.Type)
	}

	record := &kgo.Record{
		Topic: message.Destination,
		Key:   []byte(message.PartitionKey),
		Value: message.Payload,
	}

	if err := p.client.ProduceSync(ctx, record).FirstErr(); err != nil {
		return fmt.Errorf("produce %s to %s: %w", message.Type, message.Destination, err)
	}

	return nil
}

func (p *Producer) Ping(ctx context.Context) error {
	if err := p.client.Ping(ctx); err != nil {
		return fmt.Errorf("ping kafka: %w", err)
	}

	return nil
}

func (p *Producer) Close() {
	p.client.Close()
}

type Consumer struct {
	client  *kgo.Client
	backoff time.Duration
	rewound bool
}

func NewConsumer(cfg Config) (*Consumer, error) {
	switch {
	case len(cfg.Brokers) == 0:
		return nil, fmt.Errorf("%w: at least one broker is required", ErrInvalidConfig)
	case cfg.Topic == "":
		return nil, fmt.Errorf("%w: topic is required", ErrInvalidConfig)
	case cfg.Group == "":
		return nil, fmt.Errorf("%w: consumer group is required", ErrInvalidConfig)
	case cfg.RetryBackoff <= 0:
		return nil, fmt.Errorf("%w: retry backoff must be positive", ErrInvalidConfig)
	}

	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(cfg.Topic),
		kgo.DisableAutoCommit(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		return nil, fmt.Errorf("open kafka consumer: %w", err)
	}

	return &Consumer{client: client, backoff: cfg.RetryBackoff}, nil
}

func (c *Consumer) Fetch(ctx context.Context) ([]inbox.Delivery, error) {
	if c.rewound {
		c.rewound = false

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.backoff):
		}
	}

	fetches := c.client.PollRecords(ctx, 1)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if errs := fetches.Errors(); len(errs) > 0 {
		return nil, fmt.Errorf("poll %s: %w", errs[0].Topic, errs[0].Err)
	}

	records := fetches.Records()
	if len(records) == 0 {
		return nil, nil
	}

	deliveries := make([]inbox.Delivery, 0, len(records))

	for _, record := range records {
		deliveries = append(deliveries, &delivery{record: record, consumer: c})
	}

	return deliveries, nil
}

func (c *Consumer) Close() {
	c.client.Close()
}

type delivery struct {
	record   *kgo.Record
	consumer *Consumer
}

func (d *delivery) Body() []byte { return d.record.Value }

func (d *delivery) Ack(ctx context.Context) error {
	if err := d.consumer.client.CommitRecords(ctx, d.record); err != nil {
		return fmt.Errorf("commit offset %d: %w", d.record.Offset, err)
	}

	return nil
}

func (d *delivery) Drop(ctx context.Context, _ string) error {
	return d.Ack(ctx)
}

func (d *delivery) Retry(context.Context) error {
	d.consumer.client.SetOffsets(map[string]map[int32]kgo.EpochOffset{
		d.record.Topic: {
			d.record.Partition: {
				Epoch:  d.record.LeaderEpoch,
				Offset: d.record.Offset,
			},
		},
	})

	d.consumer.rewound = true

	return nil
}
