package observability

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestMain(m *testing.M) {
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {}))

	os.Exit(m.Run())
}

func testIdentity() Identity {
	return Identity{Service: "file", Component: "server"}
}

func enabledConfig() TracingConfig {
	return TracingConfig{
		Enabled:     true,
		Endpoint:    "127.0.0.1:4317",
		Insecure:    true,
		SampleRatio: 1,
		Timeout:     time.Second,
		Version:     "dev",
	}
}

func TestStartTracingSetsThePropagatorEvenWhenDisabled(t *testing.T) {
	// Arrange
	otel.SetTextMapPropagator(propagation.TextMapPropagator(nil))

	// Act
	flush, err := StartTracing(context.Background(), TracingConfig{}, testIdentity())

	// Assert
	require.NoError(t, err)
	require.NoError(t, flush(context.Background()),
		"a disabled exporter still has to answer shutdown, or the closer breaks")

	carrier := propagation.MapCarrier{}
	ctx := trace.ContextWithSpanContext(context.Background(),
		trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
			SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
		}))

	otel.GetTextMapPropagator().Inject(ctx, carrier)

	assert.Contains(t, carrier.Keys(), "traceparent",
		"an incoming trace must keep travelling even where this process exports nothing")
}

func TestStartTracingSilencesARecordingProviderWhenDisabled(t *testing.T) {
	// Arrange
	recording, err := StartTracing(context.Background(), enabledConfig(), testIdentity())
	require.NoError(t, err)

	t.Cleanup(func() { _ = recording(context.Background()) })

	_, live := otel.Tracer("test").Start(context.Background(), "before")

	require.True(t, live.IsRecording(), "the arrangement itself has to record")

	live.End()

	// Act
	_, err = StartTracing(context.Background(), TracingConfig{}, testIdentity())
	require.NoError(t, err)

	// Assert
	_, span := otel.Tracer("test").Start(context.Background(), "probe")

	defer span.End()

	assert.False(t, span.IsRecording(),
		"a disabled config must not pay for spans nobody collects")
}

func TestStartTracingDoesNotWaitForTheCollector(t *testing.T) {
	// Arrange
	cfg := enabledConfig()
	cfg.Endpoint = "127.0.0.1:1"

	started := time.Now()

	// Act
	flush, err := StartTracing(context.Background(), cfg, testIdentity())

	// Assert
	require.NoError(t, err,
		"a collector that is down must not stop the service from starting")
	assert.Less(t, time.Since(started), time.Second,
		"the exporter connects lazily, so startup must not pay for the dial")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	defer cancel()

	_ = flush(ctx)
}

func TestStartTracingRecordsWhenEnabled(t *testing.T) {
	// Arrange
	flush, err := StartTracing(context.Background(), enabledConfig(), testIdentity())
	require.NoError(t, err)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

		defer cancel()

		_ = flush(ctx)
	})

	// Act
	_, span := otel.Tracer("test").Start(context.Background(), "probe")

	defer span.End()

	// Assert
	assert.True(t, span.IsRecording())
	assert.True(t, span.SpanContext().IsSampled(),
		"ratio 1 must sample every root span")
}

func TestStartTracingRefusesABrokenConfig(t *testing.T) {
	tests := []struct {
		name   string
		id     Identity
		mutate func(*TracingConfig)
	}{
		{name: "no service", id: Identity{Component: "server"}},
		{name: "no component", id: Identity{Service: "file"}},
		{
			name: "no endpoint", id: testIdentity(),
			mutate: func(c *TracingConfig) { c.Endpoint = "" },
		},
		{
			name: "ratio above one", id: testIdentity(),
			mutate: func(c *TracingConfig) { c.SampleRatio = 1.5 },
		},
		{
			name: "negative ratio", id: testIdentity(),
			mutate: func(c *TracingConfig) { c.SampleRatio = -0.1 },
		},
		{
			name: "zero timeout", id: testIdentity(),
			mutate: func(c *TracingConfig) { c.Timeout = 0 },
		},
		{
			name: "no version", id: testIdentity(),
			mutate: func(c *TracingConfig) { c.Version = "" },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			cfg := enabledConfig()
			if test.mutate != nil {
				test.mutate(&cfg)
			}

			// Act
			flush, err := StartTracing(context.Background(), cfg, test.id)

			// Assert
			require.ErrorIs(t, err, ErrInvalidTracingConfig)
			assert.Nil(t, flush)
		})
	}
}

func TestStartTracingChecksTheIdentityBeforeAnythingElse(t *testing.T) {
	// Act
	_, err := StartTracing(context.Background(), TracingConfig{}, Identity{})

	// Assert
	require.ErrorIs(t, err, ErrInvalidTracingConfig,
		"a disabled config must not excuse a process that cannot name itself")
}
