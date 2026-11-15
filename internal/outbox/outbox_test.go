package outbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

func TestWriterRejectsInvalidMessages(t *testing.T) {
	t.Parallel()

	writer := NewWriter(pgtx.New(nil), clock.Fixed{At: time.Unix(0, 0).UTC()})

	tests := []struct {
		name    string
		message Message
	}{
		{
			name: "unknown transport",
			message: Message{
				Transport: "redis", Destination: "d", Type: "T",
			},
		},
		{
			name: "empty transport",
			message: Message{
				Destination: "d", Type: "T",
			},
		},
		{
			name: "no destination",
			message: Message{
				Transport: TransportRabbit, Type: "T",
			},
		},
		{
			name: "no type",
			message: Message{
				Transport: TransportRabbit, Destination: "d",
			},
		},
		{
			name: "kafka without a partition key",
			message: Message{
				Transport: TransportKafka, Destination: "d", Type: "T",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			err := writer.Write(context.Background(), test.message)

			// Assert
			require.ErrorIs(t, err, ErrInvalidMessage)
		})
	}
}

func TestWriterRejectsDataThatCannotBeMarshalled(t *testing.T) {
	t.Parallel()

	// Arrange
	writer := NewWriter(pgtx.New(nil), clock.Fixed{At: time.Unix(0, 0).UTC()})

	// Act
	err := writer.Write(context.Background(), Message{
		Transport:   TransportRabbit,
		Destination: "d",
		Type:        "T",
		Data:        make(chan int),
	})

	// Assert
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInvalidMessage)
	assert.Contains(t, err.Error(), "marshal T data")
}
