//go:build integration

package inbox

import (
	"context"
	"encoding/json"
	"errors"
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

func testConsumer(
	t *testing.T,
	run func(ctx context.Context, message outbox.Envelope) error,
	deliveries ...Delivery,
) (*Consumer, *fakeSource) {
	t.Helper()

	source := &fakeSource{batches: [][]Delivery{deliveries}}

	consumer, err := NewConsumer("file.worker", source, NewInbox(sharedDB),
		map[string]Handler{"ProcessFile": Repeatable(run)})
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
		})
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
		})
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
		})
	require.NoError(t, err)

	// Act
	stats, err := consumer.Consume(t.Context())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, Stats{Handled: 1}, stats)
	assert.True(t, insideTransaction,
		"a database-only effect commits together with its inbox row, that is the point")
}
