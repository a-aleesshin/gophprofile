package kafka

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/internal/outbox"
)

type stubGuard struct{ inside bool }

func (g stubGuard) InTransaction(context.Context) bool { return g.inside }

func testProducer(t *testing.T, guard TransactionGuard) *Producer {
	t.Helper()

	producer, err := NewProducer(Config{
		Brokers:      []string{"127.0.0.1:1"},
		Topic:        "gophprofile.file.v1",
		Group:        "file",
		RetryBackoff: time.Second,
	}, guard)
	require.NoError(t, err, "the client is lazy, so no broker has to answer for this")

	t.Cleanup(producer.Close)

	return producer
}

func testPending(key string) outbox.Pending {
	return outbox.Pending{
		Transport:    outbox.TransportKafka,
		Destination:  "gophprofile.file.v1",
		PartitionKey: key,
		Type:         "FileProcessed",
		Payload:      []byte(`{}`),
	}
}

func TestNewProducerRefusesAnIncompleteConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cfg   Config
		guard TransactionGuard
	}{
		{name: "no brokers", cfg: Config{Topic: "t"}, guard: stubGuard{}},
		{name: "no guard", cfg: Config{Brokers: []string{"127.0.0.1:1"}, Topic: "t"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			producer, err := NewProducer(test.cfg, test.guard)

			require.ErrorIs(t, err, ErrInvalidConfig)
			assert.Nil(t, producer)
		})
	}
}

func TestNewConsumerRefusesAnIncompleteConfig(t *testing.T) {
	t.Parallel()

	brokers := []string{"127.0.0.1:1"}

	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "no brokers", cfg: Config{Topic: "t", Group: "g", RetryBackoff: time.Second}},
		{name: "no topic", cfg: Config{Brokers: brokers, Group: "g", RetryBackoff: time.Second}},
		{name: "no group", cfg: Config{Brokers: brokers, Topic: "t", RetryBackoff: time.Second}},
		{name: "no backoff", cfg: Config{Brokers: brokers, Topic: "t", Group: "g"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			consumer, err := NewConsumer(test.cfg)

			require.ErrorIs(t, err, ErrInvalidConfig)
			assert.Nil(t, consumer)
		})
	}
}

func TestProducerRefusesToPublishInsideATransaction(t *testing.T) {
	t.Parallel()

	// Arrange
	producer := testProducer(t, stubGuard{inside: true})

	// Act
	err := producer.Send(context.Background(), testPending("user-1"))

	// Assert
	require.ErrorIs(t, err, ErrInTransaction,
		"a broker round trip would hold a database connection for its whole duration")
}

func TestProducerRefusesAFactWithoutAPartitionKey(t *testing.T) {
	t.Parallel()

	// Arrange
	producer := testProducer(t, stubGuard{})

	// Act
	err := producer.Send(context.Background(), testPending(""))

	// Assert
	require.ErrorIs(t, err, ErrNoPartitionKey,
		"without a key kafka spreads one aggregate over partitions and loses its order")
}
