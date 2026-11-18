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

func TestEnvelopeContinuesTheTraceOfWhoeverWroteIt(t *testing.T) {
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
	parent := trace.SpanContextFromContext(
		Envelope{Trace: carrier}.Context(context.Background()))

	// Assert
	assert.Equal(t, writer.TraceID(), parent.TraceID(),
		"processing has to land in the trace of the request that caused the message")
	assert.Equal(t, writer.SpanID(), parent.SpanID())
	assert.True(t, parent.IsRemote())
	assert.True(t, parent.IsSampled(),
		"the sampling decision travels with the message, or half the trace is missing")
}

func TestEnvelopeWithoutAUsableTraceKeepsTheContextAsItWas(t *testing.T) {
	t.Parallel()

	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})

	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	for name, envelope := range map[string]Envelope{
		"no field":      {},
		"empty carrier": {Trace: map[string]string{}},
		"nonsense":      {Trace: map[string]string{"traceparent": "nonsense"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			parent := trace.SpanContextFromContext(envelope.Context(context.Background()))

			assert.False(t, parent.IsValid(),
				"a carrier we cannot parse must not produce a parent pointing nowhere")
		})
	}
}

func TestWithTraceReplacesOnlyTheTraceField(t *testing.T) {
	t.Parallel()

	// Arrange
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})

	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	id := uuid.New()

	payload, err := json.Marshal(Envelope{
		MessageID: id,
		Type:      "FileProcessed",
		Trace:     map[string]string{"traceparent": "stale"},
		Data:      json.RawMessage(`{"file_id":"abc"}`),
	})
	require.NoError(t, err)

	relay := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{8, 7, 6, 5, 4, 3, 2, 1},
		TraceFlags: trace.FlagsSampled,
	})

	// Act
	rewritten := withTrace(
		trace.ContextWithSpanContext(context.Background(), relay), payload)

	// Assert
	var envelope Envelope
	require.NoError(t, json.Unmarshal(rewritten, &envelope))

	assert.Equal(t, id, envelope.MessageID)
	assert.Equal(t, "FileProcessed", envelope.Type)
	assert.JSONEq(t, `{"file_id":"abc"}`, string(envelope.Data),
		"rewriting the trace must not touch the message the handler will read")

	parent := trace.SpanContextFromContext(envelope.Context(context.Background()))

	assert.Equal(t, relay.SpanID(), parent.SpanID(),
		"the consumer has to continue from the send, not from the stale hop before it")
}

func TestWithTraceLeavesAnUntracedPayloadAlone(t *testing.T) {
	t.Parallel()

	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})

	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	payload := []byte(`{"type":"FileProcessed"}`)

	assert.Equal(t, payload, withTrace(context.Background(), payload))
	assert.Equal(t, []byte("not json"), withTrace(
		trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(
			trace.SpanContextConfig{
				TraceID:    trace.TraceID{1},
				SpanID:     trace.SpanID{2},
				TraceFlags: trace.FlagsSampled,
			})), []byte("not json")),
		"a payload we cannot parse has to travel untouched rather than be dropped")
}
