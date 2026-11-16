package outbox

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

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

func TestWriteKeepsTheTraceContextOutOfAMessageNobodyTraced(t *testing.T) {
	t.Parallel()

	// Arrange
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})

	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	// Act
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(context.Background(), carrier)

	// Assert
	assert.Empty(t, carrier,
		"without a span there is nothing to carry, and the field must stay out of the payload")

	payload, err := json.Marshal(Envelope{
		MessageID: uuid.New(),
		Type:      "FileProcessed",
		Trace:     carrier,
		Data:      json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	assert.NotContains(t, string(payload), "trace",
		"omitempty keeps the wire format of an untraced message exactly as it was")
}

func TestEnvelopeLinksBackToWhoeverWroteIt(t *testing.T) {
	t.Parallel()

	// Arrange
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})

	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	writer := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{9, 8, 7, 6, 5, 4, 3, 2, 1, 0, 1, 2, 3, 4, 5, 6},
		SpanID:     trace.SpanID{1, 1, 2, 2, 3, 3, 4, 4},
		TraceFlags: trace.FlagsSampled,
	})

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(
		trace.ContextWithSpanContext(context.Background(), writer), carrier)

	require.Contains(t, carrier, "traceparent")

	// Act
	links := Envelope{Trace: carrier}.Links()

	// Assert
	require.Len(t, links, 1)
	assert.Equal(t, writer.TraceID(), links[0].SpanContext.TraceID(),
		"the consumer has to be able to point back at the request that caused the message")
	assert.Equal(t, writer.SpanID(), links[0].SpanContext.SpanID())
}

func TestEnvelopeWithoutATraceHasNoLinks(t *testing.T) {
	t.Parallel()

	assert.Nil(t, Envelope{}.Links())
	assert.Nil(t, Envelope{Trace: map[string]string{}}.Links())
	assert.Nil(t, Envelope{Trace: map[string]string{"traceparent": "nonsense"}}.Links(),
		"a carrier we cannot parse must not produce a link to nowhere")
}
