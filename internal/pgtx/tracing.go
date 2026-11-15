package pgtx

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName       = "github.com/a-aleesshin/gophprofile/internal/pgtx"
	unknownOperation = "QUERY"
)

type queryTracer struct {
	tracer  trace.Tracer
	options []trace.SpanStartOption
}

func newQueryTracer(cfg *pgx.ConnConfig) *queryTracer {
	return &queryTracer{
		tracer: otel.Tracer(tracerName),
		options: []trace.SpanStartOption{
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				semconv.DBSystemNamePostgreSQL,
				semconv.DBNamespace(cfg.Database),
				semconv.ServerAddress(cfg.Host),
				semconv.ServerPort(int(cfg.Port)),
			),
		},
	}
}

func (t *queryTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	operation := operationOf(data.SQL)

	ctx, span := t.tracer.Start(ctx, operation, t.options...)

	if span.IsRecording() {
		span.SetAttributes(
			semconv.DBOperationName(operation),
			semconv.DBQueryText(data.SQL),
		)
	}

	return ctx
}

func (t *queryTracer) TraceQueryEnd(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryEndData,
) {
	span := trace.SpanFromContext(ctx)

	defer span.End()

	if !span.IsRecording() {
		return
	}

	if data.Err != nil {
		if !errors.Is(data.Err, pgx.ErrNoRows) {
			span.RecordError(data.Err)
			span.SetStatus(codes.Error, data.Err.Error())
		}

		return
	}

	if rows := data.CommandTag.RowsAffected(); rows > 0 {
		span.SetAttributes(semconv.DBResponseReturnedRows(int(rows)))
	}
}

func operationOf(sql string) string {
	trimmed := strings.TrimLeft(sql, " \t\r\n")

	end := strings.IndexFunc(trimmed, func(r rune) bool { return !unicode.IsLetter(r) })

	if end < 0 {
		end = len(trimmed)
	}

	if end == 0 {
		return unknownOperation
	}

	return strings.ToUpper(trimmed[:end])
}
