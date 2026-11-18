package logging

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

const (
	TraceIDKey = "trace_id"
	SpanIDKey  = "span_id"
)

type correlating struct {
	slog.Handler
}

func Correlate(handler slog.Handler) slog.Handler {
	return correlating{Handler: handler}
}

func (h correlating) Handle(ctx context.Context, record slog.Record) error {
	spanContext := trace.SpanContextFromContext(ctx)

	if spanContext.IsValid() {
		record.AddAttrs(
			slog.String(TraceIDKey, spanContext.TraceID().String()),
			slog.String(SpanIDKey, spanContext.SpanID().String()),
		)
	}

	return h.Handler.Handle(ctx, record)
}

func (h correlating) WithAttrs(attrs []slog.Attr) slog.Handler {
	return correlating{Handler: h.Handler.WithAttrs(attrs)}
}

func (h correlating) WithGroup(name string) slog.Handler {
	return correlating{Handler: h.Handler.WithGroup(name)}
}
