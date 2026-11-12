//go:build integration

package kafka

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

var brokers []string

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1")
	if err != nil {
		fmt.Fprintf(os.Stderr, "run kafka: %v\n", err)
		os.Exit(1)
	}

	brokers, err = container.Brokers(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "brokers: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if err := container.Terminate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "terminate kafka: %v\n", err)
	}

	os.Exit(code)
}

var testRuns atomic.Int64

func testRunID(t *testing.T) string {
	t.Helper()

	return strings.ReplaceAll(t.Name(), "/", ".") +
		"." + strconv.FormatInt(testRuns.Add(1), 10)
}

func testTopic(t *testing.T, id string) string {
	t.Helper()

	topic := "test." + id

	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	require.NoError(t, err)

	defer client.Close()

	admin := kadm.NewClient(client)

	created, err := admin.CreateTopics(t.Context(), 3, 1, nil, topic)
	require.NoError(t, err)
	require.NoError(t, created.Error(),
		"a topic that already exists means two runs are about to share offsets")

	return topic
}

func testConfig(t *testing.T, id, topic string) Config {
	t.Helper()

	return Config{
		Brokers:      brokers,
		Topic:        topic,
		Group:        "group." + id,
		RetryBackoff: 100 * time.Millisecond,
	}
}

func setup(t *testing.T) (Config, *Producer, *Consumer) {
	t.Helper()

	id := testRunID(t)
	cfg := testConfig(t, id, testTopic(t, id))

	producer, err := NewProducer(cfg, pgtx.New(nil))
	require.NoError(t, err)

	t.Cleanup(producer.Close)

	consumer, err := NewConsumer(cfg)
	require.NoError(t, err)

	t.Cleanup(consumer.Close)

	return cfg, producer, consumer
}

func send(t *testing.T, producer *Producer, cfg Config, key, body string) {
	t.Helper()

	require.NoError(t, producer.Send(t.Context(), outbox.Pending{
		Transport:    outbox.TransportKafka,
		Destination:  cfg.Topic,
		PartitionKey: key,
		Type:         "FileProcessed",
		Payload:      []byte(body),
	}))
}

func fetchOne(t *testing.T, consumer *Consumer, within time.Duration) inbox.Delivery {
	t.Helper()

	deadline := time.Now().Add(within)

	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		deliveries, err := consumer.Fetch(ctx)

		cancel()

		if err != nil && ctx.Err() == nil {
			require.NoError(t, err)
		}

		if len(deliveries) > 0 {
			return deliveries[0]
		}
	}

	t.Fatalf("no record within %s", within)

	return nil
}

func TestProduceAndConsume(t *testing.T) {
	// Arrange
	cfg, producer, consumer := setup(t)

	// Act
	send(t, producer, cfg, "file-1", `{"message_id":"one"}`)

	message := fetchOne(t, consumer, 30*time.Second)

	// Assert
	assert.JSONEq(t, `{"message_id":"one"}`, string(message.Body()))
	require.NoError(t, message.Ack(t.Context()))
}

func TestProduceRefusesAFactWithoutAKey(t *testing.T) {
	// Arrange
	cfg, producer, _ := setup(t)

	// Act
	err := producer.Send(t.Context(), outbox.Pending{
		Transport:   outbox.TransportKafka,
		Destination: cfg.Topic,
		Type:        "FileProcessed",
		Payload:     []byte(`{}`),
	})

	// Assert
	require.ErrorIs(t, err, ErrNoPartitionKey,
		"without a key the partition is random and order by file is lost")
}

func TestOneKeyKeepsItsOrder(t *testing.T) {
	// Arrange
	cfg, producer, consumer := setup(t)

	for _, body := range []string{`{"n":1}`, `{"n":2}`, `{"n":3}`} {
		send(t, producer, cfg, "file-1", body)
	}

	// Act
	seen := make([]string, 0, 3)

	for range 3 {
		message := fetchOne(t, consumer, 30*time.Second)
		seen = append(seen, string(message.Body()))

		require.NoError(t, message.Ack(t.Context()))
	}

	// Assert
	assert.Equal(t, []string{`{"n":1}`, `{"n":2}`, `{"n":3}`}, seen,
		"one key means one partition, and a partition is ordered")
}

func TestRetryRewindsToTheSameRecord(t *testing.T) {
	// Arrange
	cfg, producer, consumer := setup(t)

	send(t, producer, cfg, "file-1", `{"n":1}`)
	send(t, producer, cfg, "file-1", `{"n":2}`)

	first := fetchOne(t, consumer, 30*time.Second)
	require.JSONEq(t, `{"n":1}`, string(first.Body()))

	// Act
	require.NoError(t, first.Retry(t.Context()))

	again := fetchOne(t, consumer, 30*time.Second)

	// Assert
	assert.JSONEq(t, `{"n":1}`, string(again.Body()),
		"a failed record must come back before the next one, or order breaks")

	require.NoError(t, again.Ack(t.Context()))

	next := fetchOne(t, consumer, 30*time.Second)
	assert.JSONEq(t, `{"n":2}`, string(next.Body()))
	require.NoError(t, next.Ack(t.Context()))
}

func TestAckSurvivesARestart(t *testing.T) {
	// Arrange
	cfg, producer, consumer := setup(t)

	send(t, producer, cfg, "file-1", `{"n":1}`)
	send(t, producer, cfg, "file-1", `{"n":2}`)

	first := fetchOne(t, consumer, 30*time.Second)
	require.NoError(t, first.Ack(t.Context()))

	consumer.Close()

	// Act
	restarted, err := NewConsumer(cfg)
	require.NoError(t, err)

	t.Cleanup(restarted.Close)

	message := fetchOne(t, restarted, 30*time.Second)

	// Assert
	assert.JSONEq(t, `{"n":2}`, string(message.Body()),
		"a committed offset must not be served again after a restart")
}

func TestUncommittedRecordComesBackAfterARestart(t *testing.T) {
	// Arrange
	cfg, producer, consumer := setup(t)

	send(t, producer, cfg, "file-1", `{"n":1}`)

	first := fetchOne(t, consumer, 30*time.Second)
	require.JSONEq(t, `{"n":1}`, string(first.Body()))

	consumer.Close()

	// Act
	restarted, err := NewConsumer(cfg)
	require.NoError(t, err)

	t.Cleanup(restarted.Close)

	message := fetchOne(t, restarted, 30*time.Second)

	// Assert
	assert.JSONEq(t, `{"n":1}`, string(message.Body()),
		"a record whose effect was never committed must be delivered again")
}

func TestNewConsumerRefusesAnIncompleteConfig(t *testing.T) {
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
			// Act
			consumer, err := NewConsumer(test.cfg)

			// Assert
			require.ErrorIs(t, err, ErrInvalidConfig)
			assert.Nil(t, consumer)
		})
	}
}
