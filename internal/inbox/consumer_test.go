package inbox

import (
	"context"
	"strings"
	"testing"

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
