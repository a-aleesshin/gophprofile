package outbox

import (
	"context"
	"encoding/json"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const traceField = "trace"

func (e Envelope) Context(ctx context.Context) context.Context {
	parent, _ := carried(ctx, e.Trace)

	return parent
}

func carried(ctx context.Context, carrier map[string]string) (context.Context, bool) {
	if len(carrier) == 0 {
		return ctx, false
	}

	extracted := otel.GetTextMapPropagator().Extract(
		ctx, propagation.MapCarrier(carrier))

	if !trace.SpanContextFromContext(extracted).IsValid() {
		return ctx, false
	}

	return extracted, true
}

func parentOf(ctx context.Context, payload []byte) (context.Context, bool) {
	var envelope struct {
		Trace map[string]string `json:"trace"`
	}

	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ctx, false
	}

	return carried(ctx, envelope.Trace)
}

func withTrace(ctx context.Context, payload []byte) []byte {
	carrier := propagation.MapCarrier{}

	otel.GetTextMapPropagator().Inject(ctx, carrier)

	if len(carrier) == 0 {
		return payload
	}

	var fields map[string]json.RawMessage

	if err := json.Unmarshal(payload, &fields); err != nil {
		return payload
	}

	stamped, err := json.Marshal(carrier)
	if err != nil {
		return payload
	}

	fields[traceField] = stamped

	rewritten, err := json.Marshal(fields)
	if err != nil {
		return payload
	}

	return rewritten
}
