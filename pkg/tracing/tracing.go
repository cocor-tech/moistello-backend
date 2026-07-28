package tracing

import (
	"context"
	"fmt"

	"github.com/moistello/backend/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/semconv/v1.4.0"
)

var (
	tracerProvider *sdktrace.TracerProvider
)

// Init initializes the OpenTelemetry tracer provider with OTLP gRPC exporter.
func Init(cfg config.TracingConfig) error {
	if !cfg.Enabled {
		return nil
	}

	ctx := context.Background()

	// Create resource with service information
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}

	// Create OTLP gRPC exporter
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(cfg.CollectorEndpoint),
	)
	if err != nil {
		return fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Create tracer provider with sampling
	tracerProvider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(cfg.SampleRate)),
	)

	// Set global tracer provider
	otel.SetTracerProvider(tracerProvider)

	return nil
}

// Shutdown flushes any remaining spans and shuts down the tracer provider.
func Shutdown(ctx context.Context) error {
	if tracerProvider == nil {
		return nil
	}

	if err := tracerProvider.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown tracer provider: %w", err)
	}

	return nil
}

// GetTracerProvider returns the configured tracer provider.
func GetTracerProvider() *sdktrace.TracerProvider {
	return tracerProvider
}
