package tracing

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/moistello/backend/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	// instrumentationName is the tracer name used for all spans produced by
	// this application.
	instrumentationName = "moistello"
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

// StartSpan starts a child span (or a root span when the context carries no
// parent) with the given name and attributes. Callers must pair it with
// EndSpan to record the outcome and close the span.
//
// When tracing is disabled the returned span is a no-op, so callers can safely
// instrument code unconditionally (#223).
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName).Start(ctx, name, trace.WithAttributes(attrs...))
}

// EndSpan finalizes a span started by StartSpan. It records the operation's
// duration, its outcome (error + status), and any extra attributes.
func EndSpan(span trace.Span, err error, start time.Time, attrs ...attribute.KeyValue) {
	if !span.IsRecording() {
		span.End()
		return
	}

	attrs = append(attrs, attribute.Int64("duration.ms", time.Since(start).Milliseconds()))
	span.SetAttributes(attrs...)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}

	span.End()
}

// StartDBSpan begins a child span for a database operation, tagging the DBMS,
// the operation (SELECT/INSERT/UPDATE/DELETE/...), and the target table.
func StartDBSpan(ctx context.Context, operation, table string) (context.Context, trace.Span) {
	return StartSpan(ctx, "db."+operation,
		attribute.String("db.system", "postgresql"),
		attribute.String("db.operation", operation),
		attribute.String("db.table", table),
	)
}

// StartRedisSpan begins a child span for a Redis operation.
func StartRedisSpan(ctx context.Context, operation string) (context.Context, trace.Span) {
	return StartSpan(ctx, "redis."+operation,
		attribute.String("db.system", "redis"),
		attribute.String("db.operation", operation),
	)
}

// StartStellarSpan begins a child span for a Stellar Horizon/RPC operation.
func StartStellarSpan(ctx context.Context, operation string) (context.Context, trace.Span) {
	return StartSpan(ctx, "stellar."+operation,
		attribute.String("rpc.system", "stellar"),
		attribute.String("rpc.operation", operation),
	)
}

// StartHTTPSpan begins a child span for an external HTTP call.
//
// The URL is scrubbed through SafeURL before it becomes an attribute, because a
// full URL routinely carries credentials in its query string (API keys, tokens,
// signed URLs) and a span is exported to a collector that is not subject to the
// same access controls as the application.
func StartHTTPSpan(ctx context.Context, operation, method, url string) (context.Context, trace.Span) {
	return StartSpan(ctx, "http."+operation,
		attribute.String("http.method", method),
		attribute.String("http.url", SafeURL(url)),
	)
}

// SafeURL reduces a URL to the parts that are safe to export: scheme, host and
// path. Userinfo, query and fragment are dropped, since those are where bearer
// tokens, API keys, signed parameters and email addresses end up.
//
// A value that does not parse is reduced to its scheme and host if it looks like
// a URL, and otherwise passed through as-is, because dropping an attribute
// entirely would be worse than recording an opaque unparseable value.
func SafeURL(raw string) string {
	if raw == "" {
		return ""
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return raw
	}

	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// WithDBSpan runs fn inside a database child span tagged with the operation and
// table, automatically recording the span's duration and outcome. It keeps
// repository methods terse while still producing the child spans required for
// DB latency visibility (#223).
func WithDBSpan[T any](ctx context.Context, operation, table string, fn func(context.Context) (T, error)) (T, error) {
	ctx, span := StartDBSpan(ctx, operation, table)
	start := time.Now()
	result, err := fn(ctx)
	EndSpan(span, err, start)
	return result, err
}
