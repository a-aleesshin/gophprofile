package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

const (
	TransportRabbit = "rabbit"
	TransportKafka  = "kafka"
)

var ErrInvalidMessage = errors.New("invalid outbox message")

type Message struct {
	Transport    string
	Destination  string
	PartitionKey string
	Type         string
	Data         any
}

type Clock interface {
	Now() time.Time
}

type Envelope struct {
	MessageID  uuid.UUID         `json:"message_id"`
	Type       string            `json:"type"`
	OccurredAt time.Time         `json:"occurred_at"`
	Trace      map[string]string `json:"trace,omitempty"`
	Data       json.RawMessage   `json:"data"`
}

func messagingSystem(transport string) attribute.KeyValue {
	switch transport {
	case TransportKafka:
		return semconv.MessagingSystemKafka
	case TransportRabbit:
		return semconv.MessagingSystemRabbitMQ
	default:
		return semconv.MessagingSystemKey.String(transport)
	}
}

type Writer struct {
	db    *pgtx.DB
	clock Clock
}

func NewWriter(db *pgtx.DB, clock Clock) *Writer {
	return &Writer{db: db, clock: clock}
}

func (w *Writer) Write(ctx context.Context, message Message) error {
	switch {
	case message.Transport != TransportRabbit && message.Transport != TransportKafka:
		return fmt.Errorf("%w: unknown transport %q", ErrInvalidMessage, message.Transport)
	case message.Destination == "":
		return fmt.Errorf("%w: destination is required", ErrInvalidMessage)
	case message.Type == "":
		return fmt.Errorf("%w: type is required", ErrInvalidMessage)
	case message.Transport == TransportKafka && message.PartitionKey == "":
		return fmt.Errorf("%w: kafka needs a partition key to keep order", ErrInvalidMessage)
	}

	data, err := json.Marshal(message.Data)
	if err != nil {
		return fmt.Errorf("marshal %s data: %w", message.Type, err)
	}

	carrier := propagation.MapCarrier{}

	otel.GetTextMapPropagator().Inject(ctx, carrier)

	payload, err := json.Marshal(Envelope{
		MessageID:  uuid.New(),
		Type:       message.Type,
		OccurredAt: w.clock.Now(),
		Trace:      carrier,
		Data:       data,
	})
	if err != nil {
		return fmt.Errorf("marshal %s envelope: %w", message.Type, err)
	}

	var key *string

	if message.PartitionKey != "" {
		key = &message.PartitionKey
	}

	_, err = w.db.Querier(ctx).Exec(ctx, `
		INSERT INTO outbox (transport, destination, partition_key, message_type, payload)
		VALUES ($1, $2, $3, $4, $5)`,
		message.Transport, message.Destination, key, message.Type, payload)
	if err != nil {
		return fmt.Errorf("insert outbox %s: %w", message.Type, err)
	}

	return nil
}
