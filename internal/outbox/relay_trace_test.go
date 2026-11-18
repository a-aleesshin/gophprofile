package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type capturingSender struct {
	sent []Pending
	err  error
}

func (s *capturingSender) Send(_ context.Context, message Pending) error {
	s.sent = append(s.sent, message)

	return s.err
}

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)

		_ = provider.Shutdown(context.Background())
	})

	return recorder
}

func tracedPayload(t *testing.T, ctx context.Context) []byte {
	t.Helper()

	carrier := propagation.MapCarrier{}

	otel.GetTextMapPropagator().Inject(ctx, carrier)

	require.Contains(t, carrier, "traceparent")

	payload, err := json.Marshal(Envelope{
		MessageID: uuid.New(),
		Type:      "FileProcessed",
		Trace:     carrier,
		Data:      json.RawMessage(`{"file_id":"abc"}`),
	})
	require.NoError(t, err)

	return payload
}

func relayFor(sender Sender) *Relay {
	return &Relay{
		transport: TransportKafka,
		sender:    sender,
		tracer:    otel.Tracer("relay-test"),
	}
}

func TestSendKeepsTheMessageInTheTraceOfItsWriter(t *testing.T) {
	// Arrange
	recorder := recordSpans(t)

	writerCtx, writerSpan := otel.Tracer("writer-test").Start(
		context.Background(), "PATCH /api/v1/avatars")

	payload := tracedPayload(t, writerCtx)

	writerSpan.End()

	sender := &capturingSender{}

	// Act
	require.NoError(t, relayFor(sender).send(context.Background(), Pending{
		ID:          17,
		Transport:   TransportKafka,
		Destination: "gophprofile.file.v1",
		Type:        "FileProcessed",
		Payload:     payload,
	}))

	// Assert
	var sendSpan sdktrace.ReadOnlySpan

	for _, span := range recorder.Ended() {
		if span.Name() == "send gophprofile.file.v1" {
			sendSpan = span
		}
	}

	require.NotNil(t, sendSpan,
		"without a span per message the relay hop is missing from the trace")

	assert.Equal(t, writerSpan.SpanContext().TraceID(), sendSpan.SpanContext().TraceID(),
		"one message has to be one trace, from the request to the handler")
	assert.Equal(t, writerSpan.SpanContext().SpanID(), sendSpan.Parent().SpanID(),
		"the send belongs under the request that wrote the row, not under the tick")
	assert.Equal(t, trace.SpanKindProducer, sendSpan.SpanKind())

	require.Len(t, sender.sent, 1)

	var delivered Envelope
	require.NoError(t, json.Unmarshal(sender.sent[0].Payload, &delivered))

	parent := trace.SpanContextFromContext(delivered.Context(context.Background()))

	assert.Equal(t, sendSpan.SpanContext().SpanID(), parent.SpanID(),
		"the consumer continues from the send, so the send has to be the one on the wire")
	assert.JSONEq(t, `{"file_id":"abc"}`, string(delivered.Data))
}

func TestSendLinksTheMessageBackToTheTickThatCarriedIt(t *testing.T) {
	// Arrange
	recorder := recordSpans(t)

	writerCtx, writerSpan := otel.Tracer("writer-test").Start(
		context.Background(), "PATCH /api/v1/avatars")

	payload := tracedPayload(t, writerCtx)

	writerSpan.End()

	relay := relayFor(&capturingSender{})

	tickCtx, tickSpan := relay.tracer.Start(context.Background(), "outbox publish kafka")

	// Act
	require.NoError(t, relay.send(tickCtx, Pending{
		ID:          4,
		Transport:   TransportKafka,
		Destination: "gophprofile.file.v1",
		Payload:     payload,
	}))

	tickSpan.End()

	// Assert
	var sendSpan sdktrace.ReadOnlySpan

	for _, span := range recorder.Ended() {
		if span.Name() == "send gophprofile.file.v1" {
			sendSpan = span
		}
	}

	require.NotNil(t, sendSpan)
	require.Len(t, sendSpan.Links(), 1,
		"the operational view of the tick must stay reachable from the message")
	assert.Equal(t, tickSpan.SpanContext().SpanID(),
		sendSpan.Links()[0].SpanContext.SpanID())
}

func TestSendMarksTheSpanOfAMessageTheBrokerRefused(t *testing.T) {
	// Arrange
	recorder := recordSpans(t)

	writerCtx, writerSpan := otel.Tracer("writer-test").Start(
		context.Background(), "PATCH /api/v1/avatars")

	payload := tracedPayload(t, writerCtx)

	writerSpan.End()

	refused := errors.New("broker says no")

	// Act
	err := relayFor(&capturingSender{err: refused}).send(context.Background(), Pending{
		Transport:   TransportKafka,
		Destination: "gophprofile.file.v1",
		Payload:     payload,
	})

	// Assert
	require.ErrorIs(t, err, refused)

	var sendSpan sdktrace.ReadOnlySpan

	for _, span := range recorder.Ended() {
		if span.Name() == "send gophprofile.file.v1" {
			sendSpan = span
		}
	}

	require.NotNil(t, sendSpan)
	assert.Equal(t, codes.Error, sendSpan.Status().Code)
	require.Len(t, sendSpan.Events(), 1)
	assert.Equal(t, "exception", sendSpan.Events()[0].Name)
}

func TestSendLeavesAnUntracedMessageExactlyAsItIs(t *testing.T) {
	// Arrange
	recordSpans(t)

	payload := []byte(`{"type":"FileProcessed","data":{}}`)
	sender := &capturingSender{}

	// Act
	require.NoError(t, relayFor(sender).send(context.Background(), Pending{
		Transport:   TransportKafka,
		Destination: "gophprofile.file.v1",
		Payload:     payload,
	}))

	// Assert
	require.Len(t, sender.sent, 1)
	assert.Equal(t, payload, sender.sent[0].Payload,
		"a row written before the trace field existed still has to be published")
}
