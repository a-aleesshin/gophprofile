package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace/noop"
)

var ErrInvalidTracingConfig = errors.New("invalid tracing config")

type TracingConfig struct {
	Enabled     bool          `env:"ENABLED" envDefault:"false"`
	Endpoint    string        `env:"ENDPOINT" envDefault:"jaeger:4317"`
	Insecure    bool          `env:"INSECURE" envDefault:"true"`
	SampleRatio float64       `env:"SAMPLE_RATIO" envDefault:"1"`
	Timeout     time.Duration `env:"TIMEOUT" envDefault:"5s"`
	Version     string        `env:"VERSION" envDefault:"dev"`
}

type Identity struct {
	Service   string
	Component string
}

type Shutdown func(ctx context.Context) error

func StartTracing(
	ctx context.Context,
	cfg TracingConfig,
	id Identity,
) (Shutdown, error) {
	switch {
	case id.Service == "":
		return nil, fmt.Errorf("%w: service is required", ErrInvalidTracingConfig)
	case id.Component == "":
		return nil, fmt.Errorf("%w: component is required", ErrInvalidTracingConfig)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if !cfg.Enabled {
		otel.SetTracerProvider(noop.NewTracerProvider())

		return noShutdown, nil
	}

	switch {
	case cfg.Endpoint == "":
		return nil, fmt.Errorf("%w: endpoint is required", ErrInvalidTracingConfig)
	case cfg.SampleRatio < 0 || cfg.SampleRatio > 1:
		return nil, fmt.Errorf(
			"%w: sample ratio must be within [0, 1] (got %v)",
			ErrInvalidTracingConfig, cfg.SampleRatio)
	case cfg.Timeout <= 0:
		return nil, fmt.Errorf("%w: timeout must be positive", ErrInvalidTracingConfig)
	case cfg.Version == "":
		return nil, fmt.Errorf("%w: version is required", ErrInvalidTracingConfig)
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(id.Service),
		semconv.ServiceVersion(cfg.Version),
		attribute.String("component", id.Component),
	))
	if err != nil {
		return nil, fmt.Errorf("describe %s/%s: %w", id.Service, id.Component, err)
	}

	options := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		otlptracegrpc.WithTimeout(cfg.Timeout),
	}

	if cfg.Insecure {
		options = append(options, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("open the trace exporter at %s: %w", cfg.Endpoint, err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sdktrace.ParentBased(
			sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)

	otel.SetTracerProvider(provider)

	return provider.Shutdown, nil
}

func noShutdown(context.Context) error { return nil }

func CloseTracing(shutdown Shutdown, timeout time.Duration, logger *slog.Logger) func() {
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)

		defer cancel()

		if err := shutdown(ctx); err != nil {
			logger.Error("failed to flush the pending traces", "error", err)
		}
	}
}
