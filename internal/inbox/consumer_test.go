package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

type stubSource struct{}

func (stubSource) Fetch(context.Context) ([]Delivery, error) { return nil, nil }

func noopHandler(context.Context, outbox.Envelope) error { return nil }

func TestNewConsumer(t *testing.T) {
	t.Parallel()

	in := NewInbox(pgtx.New(nil))
	handlers := map[string]Handler{"ProcessFile": Repeatable(noopHandler)}

	t.Run("accepts a complete set", func(t *testing.T) {
		t.Parallel()

		// Act
		consumer, err := NewConsumer("file.worker", stubSource{}, in, handlers)

		// Assert
		require.NoError(t, err)
		assert.Equal(t, "file.worker", consumer.name)
	})

	t.Run("copies the handler map", func(t *testing.T) {
		t.Parallel()

		// Arrange
		mutable := map[string]Handler{"ProcessFile": Repeatable(noopHandler)}

		consumer, err := NewConsumer("file.worker", stubSource{}, in, mutable)
		require.NoError(t, err)

		// Act
		delete(mutable, "ProcessFile")

		// Assert
		assert.Len(t, consumer.handlers, 1)
	})

	tests := []struct {
		name     string
		consumer string
		source   Source
		inbox    *Inbox
		handlers map[string]Handler
	}{
		{name: "no name", source: stubSource{}, inbox: in, handlers: handlers},
		{
			name: "name longer than the column", consumer: strings.Repeat("x", 65),
			source: stubSource{}, inbox: in, handlers: handlers,
		},
		{name: "no source", consumer: "file.worker", inbox: in, handlers: handlers},
		{name: "no inbox", consumer: "file.worker", source: stubSource{}, handlers: handlers},
		{name: "no handlers", consumer: "file.worker", source: stubSource{}, inbox: in},
		{
			name: "handler without a type", consumer: "file.worker", source: stubSource{},
			inbox: in, handlers: map[string]Handler{"": Repeatable(noopHandler)},
		},
		{
			name: "nil handler", consumer: "file.worker", source: stubSource{},
			inbox: in, handlers: map[string]Handler{"ProcessFile": {}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			consumer, err := NewConsumer(test.consumer, test.source, test.inbox, test.handlers)

			// Assert
			require.ErrorIs(t, err, ErrInvalidConsumer)
			assert.Nil(t, consumer)
		})
	}
}

type recordedDelivery struct {
	body    []byte
	acked   int
	retried int
	dropped int
	reason  string
}

func (d *recordedDelivery) Body() []byte { return d.body }

func (d *recordedDelivery) Ack(context.Context) error {
	d.acked++

	return nil
}

func (d *recordedDelivery) Retry(context.Context) error {
	d.retried++

	return nil
}

func (d *recordedDelivery) Drop(_ context.Context, reason string) error {
	d.dropped++
	d.reason = reason

	return nil
}

type scriptedSource struct {
	deliveries []Delivery
	err        error
}

func (s *scriptedSource) Fetch(context.Context) ([]Delivery, error) {
	if s.err != nil {
		return nil, s.err
	}

	return s.deliveries, nil
}

func envelopeBytes(t *testing.T, messageID uuid.UUID, messageType string) []byte {
	t.Helper()

	payload, err := json.Marshal(outbox.Envelope{
		MessageID:  messageID,
		Type:       messageType,
		OccurredAt: time.Now().UTC(),
		Data:       json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	return payload
}

func routingConsumer(t *testing.T, source Source) *Consumer {
	t.Helper()

	consumer, err := NewConsumer("file.worker", source, NewInbox(pgtx.New(nil)),
		map[string]Handler{"FileProcessed": Repeatable(noopHandler)})
	require.NoError(t, err)

	return consumer
}

func TestHandlerCarriesItsTransactionMode(t *testing.T) {
	t.Parallel()

	assert.False(t, Repeatable(noopHandler).transactional,
		"a repeatable effect runs outside a transaction, which is what lets it do io")
	assert.True(t, Transactional(noopHandler).transactional)
}

func TestConsumeDropsWhatItCannotRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		body   []byte
		reason string
	}{
		{
			name:   "not an envelope at all",
			body:   []byte("{nonsense"),
			reason: "malformed envelope",
		},
		{
			name:   "a type nobody handles",
			body:   envelopeBytes(t, uuid.New(), "SomethingElse"),
			reason: `no handler for "SomethingElse"`,
		},
		{
			name:   "no message id to deduplicate on",
			body:   envelopeBytes(t, uuid.Nil, "FileProcessed"),
			reason: "envelope carries no message id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			delivery := &recordedDelivery{body: test.body}

			// Act
			stats, err := routingConsumer(t, &scriptedSource{
				deliveries: []Delivery{delivery},
			}).Consume(context.Background())

			// Assert
			require.NoError(t, err,
				"a message nobody can handle is not a reason to stop the consumer")
			assert.Equal(t, Stats{Dropped: 1}, stats)

			assert.Equal(t, 1, delivery.dropped)
			assert.Equal(t, test.reason, delivery.reason)
			assert.Zero(t, delivery.acked)
			assert.Zero(t, delivery.retried,
				"retrying it would occupy the ladder forever for nothing")
		})
	}
}

func TestConsumeReportsAFetchFailure(t *testing.T) {
	t.Parallel()

	// Arrange
	broken := errors.New("connection reset by peer")

	// Act
	stats, err := routingConsumer(t, &scriptedSource{err: broken}).
		Consume(context.Background())

	// Assert
	require.ErrorIs(t, err, broken)
	assert.Equal(t, Stats{}, stats)
}
