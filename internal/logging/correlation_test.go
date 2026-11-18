package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func tracedContext() (context.Context, trace.SpanContext) {
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{8, 7, 6, 5, 4, 3, 2, 1},
		TraceFlags: trace.FlagsSampled,
	})

	return trace.ContextWithSpanContext(context.Background(), spanContext), spanContext
}

func entry(t *testing.T, buffer *bytes.Buffer) map[string]any {
	t.Helper()

	var record map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buffer.Bytes()), &record))

	return record
}

func TestCorrelateStampsTheTraceOnTheLine(t *testing.T) {
	t.Parallel()

	// Arrange
	var buffer bytes.Buffer

	logger, err := New(Config{Level: "info", Format: FormatJSON}, &buffer)
	require.NoError(t, err)

	ctx, spanContext := tracedContext()

	// Act
	logger.InfoContext(ctx, "uploading avatar", "size_bytes", 1024)

	// Assert
	record := entry(t, &buffer)
	assert.Equal(t, spanContext.TraceID().String(), record[TraceIDKey],
		"this is the whole point: the line and the trace have to be joinable")
	assert.Equal(t, spanContext.SpanID().String(), record[SpanIDKey])
	assert.Equal(t, float64(1024), record["size_bytes"])
}

func TestCorrelateLeavesAnUntracedLineAlone(t *testing.T) {
	t.Parallel()

	// Arrange
	var buffer bytes.Buffer

	logger, err := New(Config{Level: "info", Format: FormatJSON}, &buffer)
	require.NoError(t, err)

	// Act
	logger.InfoContext(context.Background(), "starting")

	// Assert
	record := entry(t, &buffer)
	assert.NotContains(t, record, TraceIDKey,
		"an empty trace id is worse than none: it looks like a value and groups every line together")
	assert.NotContains(t, record, SpanIDKey)
}

func TestCorrelateSurvivesWith(t *testing.T) {
	t.Parallel()

	// Arrange
	var buffer bytes.Buffer

	logger, err := New(Config{Level: "info", Format: FormatJSON}, &buffer)
	require.NoError(t, err)

	ctx, spanContext := tracedContext()

	// Act
	logger.With("service", "file", "component", "worker").
		InfoContext(ctx, "reconciled uploads")

	// Assert
	record := entry(t, &buffer)
	assert.Equal(t, "file", record["service"])
	assert.Equal(t, spanContext.TraceID().String(), record[TraceIDKey],
		"every process calls With at startup, so losing the decoration there loses it everywhere")
}

func TestCorrelateSurvivesWithGroupButLandsInsideIt(t *testing.T) {
	t.Parallel()

	// Arrange
	var buffer bytes.Buffer

	logger, err := New(Config{Level: "info", Format: FormatJSON}, &buffer)
	require.NoError(t, err)

	ctx, spanContext := tracedContext()

	// Act
	logger.WithGroup("upload").InfoContext(ctx, "done", "bytes", 1)

	// Assert
	record := entry(t, &buffer)

	group, nested := record["upload"].(map[string]any)
	require.True(t, nested)

	assert.Equal(t, spanContext.TraceID().String(), group[TraceIDKey],
		"a decorator adds to the record, and slog puts record attrs into the open group, "+
			"so under a group the id is nested rather than lost")
	assert.NotContains(t, record, TraceIDKey)
	assert.Equal(t, float64(1), group["bytes"])
}

func TestCorrelateCannotHelpACallWithoutAContext(t *testing.T) {
	t.Parallel()

	// Arrange
	var buffer bytes.Buffer

	logger, err := New(Config{Level: "info", Format: FormatJSON}, &buffer)
	require.NoError(t, err)

	ctx, _ := tracedContext()
	_ = ctx

	// Act
	logger.Info("http request")

	// Assert
	assert.NotContains(t, entry(t, &buffer), TraceIDKey,
		"slog.Info carries no context, which is why the hot paths had to move to InfoContext")
}

func TestCorrelateKeepsTheTextFormatUsable(t *testing.T) {
	t.Parallel()

	// Arrange
	var buffer bytes.Buffer

	logger, err := New(Config{Level: "info", Format: FormatText}, &buffer)
	require.NoError(t, err)

	ctx, spanContext := tracedContext()

	// Act
	logger.InfoContext(ctx, "looking around")

	// Assert
	assert.Contains(t, buffer.String(), TraceIDKey+"="+spanContext.TraceID().String())
}

func TestCorrelateRespectsTheLevel(t *testing.T) {
	t.Parallel()

	// Arrange
	var buffer bytes.Buffer

	logger, err := New(Config{Level: "warn", Format: FormatJSON}, &buffer)
	require.NoError(t, err)

	ctx, _ := tracedContext()

	// Act
	logger.InfoContext(ctx, "ignored")

	// Assert
	assert.Empty(t, buffer.String(),
		"the decorator must not make a filtered line pay for formatting")
}

var _ slog.Handler = correlating{}
