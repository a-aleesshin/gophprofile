//go:build integration

package rabbitmq

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcrabbit "github.com/testcontainers/testcontainers-go/modules/rabbitmq"

	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

const (
	testRoutingKey = "file.process"
	testTag        = "file.worker"
)

var brokerURL string

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tcrabbit.Run(ctx, "rabbitmq:4-management-alpine")
	if err != nil {
		fmt.Fprintf(os.Stderr, "run rabbitmq: %v\n", err)
		os.Exit(1)
	}

	brokerURL, err = container.AmqpURL(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "amqp url: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if err := container.Terminate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "terminate rabbitmq: %v\n", err)
	}

	os.Exit(code)
}

func testConfig(t *testing.T) Config {
	t.Helper()

	return Config{
		URL:      brokerURL,
		Exchange: "test." + t.Name(),
		Prefetch: 3,
		Ladder:   []time.Duration{200 * time.Millisecond, 400 * time.Millisecond},
	}
}

func dial(t *testing.T) *amqp.Connection {
	t.Helper()

	conn, err := amqp.Dial(brokerURL)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

func setup(t *testing.T) (*amqp.Connection, Config, *Publisher, *Consumer) {
	t.Helper()

	conn := dial(t)
	cfg := testConfig(t)

	require.NoError(t, Declare(t.Context(), conn, cfg, []string{testRoutingKey}))

	publisher, err := NewPublisher(conn, cfg, pgtx.New(nil))
	require.NoError(t, err)

	t.Cleanup(func() { _ = publisher.Close() })

	consumer, err := NewConsumer(conn, cfg, testRoutingKey, testTag)
	require.NoError(t, err)

	t.Cleanup(func() { _ = consumer.Close() })

	return conn, cfg, publisher, consumer
}

func send(t *testing.T, publisher *Publisher, body string) {
	t.Helper()

	require.NoError(t, publisher.Send(t.Context(), outbox.Pending{
		Transport:   outbox.TransportRabbit,
		Destination: testRoutingKey,
		Type:        "ProcessFile",
		Payload:     []byte(body),
	}))
}

func fetchOne(t *testing.T, consumer *Consumer, within time.Duration) inbox.Delivery {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), within)
	defer cancel()

	deliveries, err := consumer.Fetch(ctx)
	require.NoError(t, err)
	require.Len(t, deliveries, 1)

	return deliveries[0]
}

func drain(t *testing.T, conn *amqp.Connection, queue string) []amqp.Delivery {
	t.Helper()

	channel, err := conn.Channel()
	require.NoError(t, err)

	defer func() { _ = channel.Close() }()

	var collected []amqp.Delivery

	for {
		message, ok, err := channel.Get(queue, true)
		require.NoError(t, err)

		if !ok {
			return collected
		}

		collected = append(collected, message)
	}
}

func TestPublishAndConsume(t *testing.T) {
	// Arrange
	_, _, publisher, consumer := setup(t)

	// Act
	send(t, publisher, `{"message_id":"one"}`)

	message := fetchOne(t, consumer, 5*time.Second)

	// Assert
	assert.JSONEq(t, `{"message_id":"one"}`, string(message.Body()))
	require.NoError(t, message.Ack(t.Context()))
}

func TestPublishRefusesAnUnroutableDestination(t *testing.T) {
	// Arrange
	_, _, publisher, _ := setup(t)

	// Act
	err := publisher.Send(t.Context(), outbox.Pending{
		Transport:   outbox.TransportRabbit,
		Destination: "nobody.listens.here",
		Type:        "ProcessFile",
		Payload:     []byte(`{}`),
	})

	// Assert
	require.ErrorIs(t, err, ErrUnroutable,
		"a confirmed publish that reached no queue is a lost command, not a success")
}

func TestRetryComesBackAfterTheDelay(t *testing.T) {
	// Arrange
	_, _, publisher, consumer := setup(t)

	send(t, publisher, `{"message_id":"two"}`)

	first := fetchOne(t, consumer, 5*time.Second)

	// Act
	require.NoError(t, first.Retry(t.Context()))

	second := fetchOne(t, consumer, 5*time.Second)

	// Assert
	assert.JSONEq(t, `{"message_id":"two"}`, string(second.Body()))

	redelivered, ok := second.(*delivery)
	require.True(t, ok)
	assert.Equal(t, 1, attemptOf(redelivered.message.Headers),
		"the attempt counter must survive the trip through the delay queue")
	assert.Equal(t, testRoutingKey, redelivered.message.RoutingKey,
		"the routing key must survive, or the retry lands in the wrong queue")

	require.NoError(t, second.Ack(t.Context()))
}

func TestRetryGivesUpAtTheEndOfTheLadder(t *testing.T) {
	// Arrange
	conn, cfg, publisher, consumer := setup(t)

	send(t, publisher, `{"message_id":"three"}`)

	// Act
	for attempt := range len(cfg.Ladder) + 1 {
		message := fetchOne(t, consumer, 5*time.Second)

		current, ok := message.(*delivery)
		require.True(t, ok)
		assert.Equal(t, attempt, attemptOf(current.message.Headers))

		require.NoError(t, message.Retry(t.Context()))
	}

	// Assert
	var dead []amqp.Delivery

	require.Eventually(t, func() bool {
		dead = drain(t, conn, cfg.deadLetterQueue())

		return len(dead) == 1
	}, 10*time.Second, 100*time.Millisecond)

	assert.Contains(t, dead[0].Headers[headerDropReason], "gave up")
	assert.Equal(t, testRoutingKey, dead[0].Headers[headerOriginalKey])
}

func TestDropGoesStraightToTheDeadLetterQueue(t *testing.T) {
	// Arrange
	conn, cfg, publisher, consumer := setup(t)

	send(t, publisher, `{"message_id":"four"}`)

	message := fetchOne(t, consumer, 5*time.Second)

	// Act
	require.NoError(t, message.Drop(t.Context(), "no handler for ProcessFile"))

	// Assert
	var dead []amqp.Delivery

	require.Eventually(t, func() bool {
		dead = drain(t, conn, cfg.deadLetterQueue())

		return len(dead) == 1
	}, 10*time.Second, 100*time.Millisecond)

	assert.Equal(t, "no handler for ProcessFile", dead[0].Headers[headerDropReason])
	assert.JSONEq(t, `{"message_id":"four"}`, string(dead[0].Body))
}

func TestFetchStopsWhenTheContextIsDone(t *testing.T) {
	// Arrange
	_, _, _, consumer := setup(t)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	// Act
	deliveries, err := consumer.Fetch(ctx)

	// Assert
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, deliveries)
}
