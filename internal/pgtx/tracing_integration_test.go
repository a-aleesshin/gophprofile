//go:build integration

package pgtx

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)

	t.Cleanup(func() {
		otel.SetTracerProvider(previous)

		_ = provider.Shutdown(context.Background())
	})

	return recorder
}

func tracedPool(t *testing.T) *DB {
	t.Helper()

	pool, err := Connect(t.Context(), Config{
		DSN:             sharedDSN,
		MaxConns:        2,
		MinConns:        1,
		MaxConnLifetime: time.Hour,
		MaxConnIdleTime: time.Minute,
		ConnectTimeout:  5 * time.Second,
	})
	require.NoError(t, err)

	t.Cleanup(pool.Close)

	return New(pool)
}

func spanNamed(
	t *testing.T,
	recorder *tracetest.SpanRecorder,
	name string,
) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range recorder.Ended() {
		if span.Name() == name {
			return span
		}
	}

	names := make([]string, 0, len(recorder.Ended()))
	for _, span := range recorder.Ended() {
		names = append(names, span.Name())
	}

	require.FailNowf(t, "span not found", "wanted %q, recorded %v", name, names)

	return nil
}

func attributesOf(span sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	found := make(map[attribute.Key]attribute.Value, len(span.Attributes()))

	for _, kv := range span.Attributes() {
		found[kv.Key] = kv.Value
	}

	return found
}

func TestAQueryGetsItsOwnSpanUnderTheCaller(t *testing.T) {
	// Arrange
	recorder := recordSpans(t)
	db := tracedPool(t)

	ctx, caller := otel.Tracer("test").Start(t.Context(), "caller")

	// Act
	var one int
	require.NoError(t, db.Querier(ctx).QueryRow(ctx, `SELECT 1`).Scan(&one))

	caller.End()

	// Assert
	span := spanNamed(t, recorder, "SELECT")

	assert.Equal(t, trace.SpanKindClient, span.SpanKind())
	assert.Equal(t, caller.SpanContext().SpanID(), span.Parent().SpanID(),
		"a query belongs under the request that asked for it, or the trace says nothing about why it ran")

	attributes := attributesOf(span)
	assert.Equal(t, "postgresql", attributes["db.system.name"].AsString())
	assert.Equal(t, "pgtx", attributes["db.namespace"].AsString())
	assert.Equal(t, "SELECT", attributes["db.operation.name"].AsString())
	assert.Equal(t, "SELECT 1", attributes["db.query.text"].AsString())
	assert.NotEmpty(t, attributes["server.address"].AsString())
}

func TestAQuerySpanNeverCarriesTheParameters(t *testing.T) {
	// Arrange
	recorder := recordSpans(t)
	db := tracedPool(t)

	const secret = "ivan-petrov-0x5ecret"

	// Act
	var echoed string
	require.NoError(t, db.Querier(t.Context()).
		QueryRow(t.Context(), `SELECT $1::text`, secret).Scan(&echoed))

	require.Equal(t, secret, echoed, "the query itself has to have run")

	// Assert
	span := spanNamed(t, recorder, "SELECT")

	assert.Equal(t, "SELECT $1::text", attributesOf(span)["db.query.text"].AsString(),
		"the statement is static, so it is safe to record; the values are not")

	for _, kv := range span.Attributes() {
		assert.NotContains(t, fmt.Sprint(kv.Value.AsInterface()), secret,
			"a parameter value in an attribute would ship user data into every trace")
	}
}

func TestAFailedQueryMarksItsSpan(t *testing.T) {
	// Arrange
	recorder := recordSpans(t)
	db := tracedPool(t)

	// Act
	_, err := db.Querier(t.Context()).Exec(t.Context(), `SELECT * FROM nothing_here`)
	require.Error(t, err)

	// Assert
	span := spanNamed(t, recorder, "SELECT")

	assert.Equal(t, "Error", span.Status().Code.String())
	assert.NotEmpty(t, span.Events(), "the error itself belongs on the span, not only the status")
}

func TestATransactionShowsItsBoundaries(t *testing.T) {
	// Arrange
	recorder := recordSpans(t)
	db := tracedPool(t)

	// Act
	require.NoError(t, db.InTx(t.Context(), func(ctx context.Context) error {
		_, err := db.Querier(ctx).Exec(ctx, `SELECT 1`)

		return err
	}))

	// Assert
	names := make([]string, 0, len(recorder.Ended()))
	for _, span := range recorder.Ended() {
		names = append(names, span.Name())
	}

	assert.Contains(t, names, "BEGIN",
		"a transaction that opens and commits is two statements, and both cost time")
	assert.Contains(t, names, "COMMIT")
}

func TestABatchGetsOneSpanUnderTheCaller(t *testing.T) {
	// Arrange
	recorder := recordSpans(t)
	db := tracedPool(t)

	batch := &pgx.Batch{}

	batch.Queue(`SELECT 1`)
	batch.Queue(`SELECT 2`)
	batch.Queue(`SELECT 3`)

	ctx, caller := otel.Tracer("test").Start(t.Context(), "caller")

	// Act
	results := db.Querier(ctx).SendBatch(ctx, batch)
	require.NoError(t, results.Close())

	caller.End()

	// Assert
	span := spanNamed(t, recorder, "BATCH")

	assert.Equal(t, caller.SpanContext().SpanID(), span.Parent().SpanID(),
		"a batch is one round trip of the caller, not a trace of its own")
	assert.Equal(t, int64(3),
		attributesOf(span)[semconv.DBOperationBatchSizeKey].AsInt64(),
		"the size is the whole point: it says how much one round trip carried")
}

func TestAFailedBatchMarksItsSpan(t *testing.T) {
	// Arrange
	recorder := recordSpans(t)
	db := tracedPool(t)

	batch := &pgx.Batch{}

	batch.Queue(`SELECT 1`)
	batch.Queue(`SELECT 1 / 0`)

	// Act
	results := db.Querier(t.Context()).SendBatch(t.Context(), batch)

	_, firstErr := results.Exec()
	_, secondErr := results.Exec()

	_ = results.Close()

	// Assert
	require.NoError(t, firstErr)
	require.Error(t, secondErr)

	span := spanNamed(t, recorder, "BATCH")

	assert.Equal(t, codes.Error, span.Status().Code)
	require.NotEmpty(t, span.Events(),
		"a batch can outlive one broken statement, so the failing one has to be named")
	assert.Equal(t, "exception", span.Events()[0].Name,
		"the span has to carry which statement broke, not only that something did")
}
