//go:build integration

package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/internal/outbox"
)

type fakeDelivery struct {
	body    []byte
	acked   int
	retried int
	dropped int
	reason  string
	err     error
}

func (d *fakeDelivery) Body() []byte { return d.body }

func (d *fakeDelivery) Ack(context.Context) error {
	d.acked++

	return d.err
}

func (d *fakeDelivery) Retry(context.Context) error {
	d.retried++

	return d.err
}

func (d *fakeDelivery) Drop(_ context.Context, reason string) error {
	d.dropped++
	d.reason = reason

	return d.err
}

type fakeSource struct {
	batches [][]Delivery
	calls   int
	err     error
}

func (s *fakeSource) Fetch(context.Context) ([]Delivery, error) {
	if s.err != nil {
		return nil, s.err
	}

	if s.calls >= len(s.batches) {
		s.calls++

		return nil, nil
	}

	batch := s.batches[s.calls]
	s.calls++

	return batch, nil
}

func envelopeBody(t *testing.T, id uuid.UUID, messageType string) []byte {
	t.Helper()

	body, err := json.Marshal(outbox.Envelope{
		MessageID:  id,
		Type:       messageType,
		OccurredAt: time.Now().UTC(),
		Data:       json.RawMessage(`{"file_id":"x"}`),
	})
	require.NoError(t, err)

	return body
}

const testMaxAttempts = 3

func testConsumer(
	t *testing.T,
	run func(ctx context.Context, message outbox.Envelope) error,
	deliveries ...Delivery,
) (*Consumer, *fakeSource) {
	t.Helper()

	source := &fakeSource{batches: [][]Delivery{deliveries}}

	consumer, err := NewConsumer("file.worker", source, NewInbox(sharedDB),
		map[string]Handler{"ProcessFile": Repeatable(run)}, testMaxAttempts)
	require.NoError(t, err)

	return consumer, source
}

func TestConsumerHandlesAndAcks(t *testing.T) {
	// Arrange
	var seen outbox.Envelope

	id := uuid.New()
	delivery := &fakeDelivery{body: envelopeBody(t, id, "ProcessFile")}

	consumer, _ := testConsumer(t, func(_ context.Context, message outbox.Envelope) error {
		seen = message

		return nil
	}, delivery)

	// Act
	stats, err := consumer.Consume(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, Stats{Handled: 1}, stats)
	assert.Equal(t, 1, delivery.acked)
	assert.Equal(t, id, seen.MessageID)
	assert.JSONEq(t, `{"file_id":"x"}`, string(seen.Data))
}

func TestConsumerAcksARedeliveryWithoutRunningTheHandler(t *testing.T) {
	// Arrange
	id := uuid.New()
	runs := 0

	handler := func(context.Context, outbox.Envelope) error {
		runs++

		return nil
	}

	first := &fakeDelivery{body: envelopeBody(t, id, "ProcessFile")}
	second := &fakeDelivery{body: envelopeBody(t, id, "ProcessFile")}

	consumer, _ := testConsumer(t, handler, first)
	_, err := consumer.Consume(t.Context())
	require.NoError(t, err)

	again, _ := testConsumer(t, handler, second)

	// Act
	stats, err := again.Consume(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, Stats{Duplicate: 1}, stats)
	assert.Equal(t, 1, second.acked, "a duplicate is settled, not retried")
	assert.Equal(t, 1, runs)
}

func TestConsumerRetriesATransientFailure(t *testing.T) {
	// Arrange
	delivery := &fakeDelivery{body: envelopeBody(t, uuid.New(), "ProcessFile")}

	consumer, _ := testConsumer(t, func(context.Context, outbox.Envelope) error {
		return errors.New("s3 is unreachable")
	}, delivery)

	// Act
	stats, err := consumer.Consume(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, Stats{Retried: 1}, stats)
	assert.Equal(t, 1, delivery.retried)
	assert.Zero(t, delivery.acked)
}

func TestConsumerDropsWhatCanNeverBeProcessed(t *testing.T) {
	id := uuid.New()

	tests := []struct {
		name     string
		delivery *fakeDelivery
		handler  func(ctx context.Context, message outbox.Envelope) error
		reason   string
	}{
		{
			name:     "malformed envelope",
			delivery: &fakeDelivery{body: []byte("{not json")},
			reason:   "malformed envelope",
		},
		{
			name:     "unknown type",
			delivery: &fakeDelivery{body: envelopeBody(t, id, "SomethingElse")},
			reason:   `no handler for "SomethingElse"`,
		},
		{
			name:     "no message id",
			delivery: &fakeDelivery{body: envelopeBody(t, uuid.Nil, "ProcessFile")},
			reason:   "envelope carries no message id",
		},
		{
			name:     "handler says it is unprocessable",
			delivery: &fakeDelivery{body: envelopeBody(t, uuid.New(), "ProcessFile")},
			handler: func(context.Context, outbox.Envelope) error {
				return ErrUnprocessable
			},
			reason: ErrUnprocessable.Error(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			handler := test.handler
			if handler == nil {
				handler = func(context.Context, outbox.Envelope) error {
					t.Fatal("the handler must not be called")

					return nil
				}
			}

			consumer, _ := testConsumer(t, handler, test.delivery)

			// Act
			stats, err := consumer.Consume(t.Context())

			// Assert
			require.NoError(t, err)
			assert.Equal(t, Stats{Dropped: 1}, stats)
			assert.Equal(t, 1, test.delivery.dropped)
			assert.Contains(t, test.delivery.reason, test.reason)
			assert.Zero(t, test.delivery.acked)
			assert.Zero(t, test.delivery.retried)
		})
	}
}

func TestConsumerDoesNotSettleWhenTheContextIsCancelled(t *testing.T) {
	// Arrange
	ctx, cancel := context.WithCancel(context.Background())
	delivery := &fakeDelivery{body: envelopeBody(t, uuid.New(), "ProcessFile")}

	consumer, _ := testConsumer(t, func(context.Context, outbox.Envelope) error {
		cancel()

		return errors.New("interrupted")
	}, delivery)

	// Act
	_, err := consumer.Consume(ctx)

	// Assert
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, delivery.acked)
	assert.Zero(t, delivery.retried)
	assert.Zero(t, delivery.dropped,
		"a shutdown must leave the message for the next run, not quarantine it")
}

func TestConsumerReportsAFetchFailure(t *testing.T) {
	// Arrange
	broken := errors.New("rabbit channel closed")
	source := &fakeSource{err: broken}

	consumer, err := NewConsumer("file.worker", source, NewInbox(sharedDB),
		map[string]Handler{
			"ProcessFile": Repeatable(func(context.Context, outbox.Envelope) error { return nil }),
		}, testMaxAttempts)
	require.NoError(t, err)

	// Act
	_, err = consumer.Consume(t.Context())

	// Assert
	require.ErrorIs(t, err, broken)
	assert.ErrorContains(t, err, "file.worker")
}

func TestConsumerRunStopsOnTheFirstFailure(t *testing.T) {
	// Arrange
	broken := errors.New("rabbit channel closed")

	consumer, err := NewConsumer("file.worker", &fakeSource{err: broken}, NewInbox(sharedDB),
		map[string]Handler{
			"ProcessFile": Repeatable(func(context.Context, outbox.Envelope) error { return nil }),
		}, testMaxAttempts)
	require.NoError(t, err)

	// Act
	err = consumer.Run(t.Context())

	// Assert
	require.ErrorIs(t, err, broken)
}

func TestConsumerRunsARepeatableHandlerOutsideATransaction(t *testing.T) {
	// Arrange
	var insideTransaction bool

	delivery := &fakeDelivery{body: envelopeBody(t, uuid.New(), "ProcessFile")}

	consumer, _ := testConsumer(t, func(ctx context.Context, _ outbox.Envelope) error {
		insideTransaction = sharedDB.InTransaction(ctx)

		return nil
	}, delivery)

	// Act
	stats, err := consumer.Consume(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, Stats{Handled: 1}, stats)
	assert.Equal(t, 1, delivery.acked)
	assert.False(t, insideTransaction,
		"process file reads s3, decodes and resizes, none of which belongs in a transaction")
}

func TestConsumerRunsATransactionalHandlerInsideATransaction(t *testing.T) {
	// Arrange
	var insideTransaction bool

	delivery := &fakeDelivery{body: envelopeBody(t, uuid.New(), "FileProcessed")}
	source := &fakeSource{batches: [][]Delivery{{delivery}}}

	consumer, err := NewConsumer("profile.worker", source, NewInbox(sharedDB),
		map[string]Handler{
			"FileProcessed": Transactional(func(ctx context.Context, _ outbox.Envelope) error {
				insideTransaction = sharedDB.InTransaction(ctx)

				return nil
			}),
		}, testMaxAttempts)
	require.NoError(t, err)

	// Act
	stats, err := consumer.Consume(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, Stats{Handled: 1}, stats)
	assert.True(t, insideTransaction,
		"a database-only effect commits together with its inbox row, that is the point")
}

func failureRow(t *testing.T, consumer string, id uuid.UUID) (int, string, bool) {
	t.Helper()

	var (
		attempts  int
		lastError string
		dropped   bool
	)

	err := sharedDB.Pool().QueryRow(t.Context(), `
		SELECT attempts, last_error, dropped_at IS NOT NULL
		  FROM failed_messages WHERE consumer = $1 AND message_id = $2`,
		consumer, id).Scan(&attempts, &lastError, &dropped)
	require.NoError(t, err)

	return attempts, lastError, dropped
}

func TestConsumerRetriesUntilTheAttemptsRunOut(t *testing.T) {
	// Arrange
	id := uuid.New()
	boom := errors.New("duplicate key value violates unique constraint")

	run := func(context.Context, outbox.Envelope) error { return boom }

	// Act
	outcomes := make([]Stats, 0, testMaxAttempts)
	deliveries := make([]*fakeDelivery, 0, testMaxAttempts)

	for range testMaxAttempts {
		delivery := &fakeDelivery{body: envelopeBody(t, id, "ProcessFile")}
		deliveries = append(deliveries, delivery)

		consumer, _ := testConsumer(t, run, delivery)

		stats, err := consumer.Consume(t.Context())
		require.NoError(t, err,
			"a message the handler cannot apply is not a reason to stop the consumer")

		outcomes = append(outcomes, stats)
	}

	// Assert
	assert.Equal(t, []Stats{
		{Retried: 1},
		{Retried: 1},
		{Exhausted: 1},
	}, outcomes, "the last attempt settles the message instead of handing it back")

	assert.Equal(t, 1, deliveries[0].retried)
	assert.Equal(t, 1, deliveries[1].retried)
	assert.Zero(t, deliveries[2].retried,
		"a rewind here would put the partition back where it started")
	assert.Equal(t, 1, deliveries[2].dropped)
	assert.Contains(t, deliveries[2].reason, "gave up after 3 attempts")
	assert.Contains(t, deliveries[2].reason, "duplicate key")

	attempts, lastError, dropped := failureRow(t, "file.worker", id)
	assert.Equal(t, testMaxAttempts, attempts)
	assert.True(t, dropped, "the row is the incident record, so it must say it was given up on")
	assert.Contains(t, lastError, "duplicate key")
}

func TestConsumerCountsAttemptsAcrossRestarts(t *testing.T) {
	// Arrange
	id := uuid.New()
	run := func(context.Context, outbox.Envelope) error { return errors.New("still broken") }

	first := &fakeDelivery{body: envelopeBody(t, id, "ProcessFile")}
	consumerA, _ := testConsumer(t, run, first)

	_, err := consumerA.Consume(t.Context())
	require.NoError(t, err)

	attempts, _, _ := failureRow(t, "file.worker", id)
	require.Equal(t, 1, attempts)

	// Act
	second := &fakeDelivery{body: envelopeBody(t, id, "ProcessFile")}
	consumerB, _ := testConsumer(t, run, second)

	stats, err := consumerB.Consume(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, Stats{Retried: 1}, stats)

	attempts, _, dropped := failureRow(t, "file.worker", id)
	assert.Equal(t, 2, attempts,
		"the counter lives in the database because Kafka carries nothing of its own, "+
			"so a restart must not hand the message a fresh set of attempts")
	assert.False(t, dropped)
}

func TestConsumerKeepsCountPerMessage(t *testing.T) {
	// Arrange
	poisoned, healthy := uuid.New(), uuid.New()

	run := func(_ context.Context, message outbox.Envelope) error {
		if message.MessageID == poisoned {
			return errors.New("poison")
		}

		return nil
	}

	// Act
	for range testMaxAttempts {
		consumer, _ := testConsumer(t, run,
			&fakeDelivery{body: envelopeBody(t, poisoned, "ProcessFile")})

		_, err := consumer.Consume(t.Context())
		require.NoError(t, err)
	}

	good := &fakeDelivery{body: envelopeBody(t, healthy, "ProcessFile")}
	consumer, _ := testConsumer(t, run, good)

	stats, err := consumer.Consume(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, Stats{Handled: 1}, stats,
		"a neighbour of a poisoned message must not inherit its attempts")
	assert.Equal(t, 1, good.acked)

	var rows int
	require.NoError(t, sharedDB.Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM failed_messages WHERE message_id = $1`, healthy).Scan(&rows))
	assert.Zero(t, rows, "the counter is keyed by message id, not by consumer")
}

func TestConsumerStillDropsWhatCanNeverBeProcessed(t *testing.T) {
	// Arrange
	id := uuid.New()
	delivery := &fakeDelivery{body: envelopeBody(t, id, "ProcessFile")}

	run := func(context.Context, outbox.Envelope) error {
		return fmt.Errorf("%w: file id is not a uuid", ErrUnprocessable)
	}

	consumer, _ := testConsumer(t, run, delivery)

	// Act
	stats, err := consumer.Consume(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, Stats{Dropped: 1}, stats,
		"a message that can never be applied does not need to spend attempts first")
	assert.Equal(t, 1, delivery.dropped)
	assert.Zero(t, delivery.retried)

	var rows int
	require.NoError(t, sharedDB.Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM failed_messages WHERE message_id = $1`, id).Scan(&rows))
	assert.Zero(t, rows)
}
