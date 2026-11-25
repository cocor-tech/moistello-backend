package logger

import (
	"bytes"
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestCtx_IncludesRequestIDAndUserID(t *testing.T) {
	var buf bytes.Buffer
	original := log.Logger
	log.Logger = log.Output(&buf)
	t.Cleanup(func() { log.Logger = original })

	ctx := context.WithValue(context.Background(), RequestIDKey, "req-123")
	ctx = context.WithValue(ctx, UserIDKey, "user-456")

	l := Ctx(ctx)
	l.Info().Msg("hello")

	out := buf.String()
	require.Contains(t, out, "requestID")
	require.Contains(t, out, "req-123")
	require.Contains(t, out, "userID")
	require.Contains(t, out, "user-456")
}

func TestCtx_NoContextFieldsWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	original := log.Logger
	log.Logger = log.Output(&buf)
	t.Cleanup(func() { log.Logger = original })

	l := Ctx(context.Background())
	l.Info().Msg("bare")

	out := buf.String()
	require.NotContains(t, out, "requestID")
	require.NotContains(t, out, "userID")
}

func TestCtx_NilContext(t *testing.T) {
	var buf bytes.Buffer
	original := log.Logger
	log.Logger = log.Output(&buf)
	t.Cleanup(func() { log.Logger = original })

	l := Ctx(nil)
	l.Info().Msg("nil-context")

	out := buf.String()
	require.Contains(t, out, "nil-context")
}

func TestCtx_EmptyOrNonStringValues(t *testing.T) {
	var buf bytes.Buffer
	original := log.Logger
	log.Logger = log.Output(&buf)
	t.Cleanup(func() { log.Logger = original })

	ctx := context.WithValue(context.Background(), RequestIDKey, "")
	ctx = context.WithValue(ctx, UserIDKey, 12345) // not a string

	l := Ctx(ctx)
	l.Info().Msg("empty-test")

	out := buf.String()
	require.NotContains(t, out, "requestID")
	require.NotContains(t, out, "userID")
}

func TestSetLevel(t *testing.T) {
	orig := zerolog.GlobalLevel()
	t.Cleanup(func() { zerolog.SetGlobalLevel(orig) })

	tests := []struct {
		input    string
		expected zerolog.Level
	}{
		{"debug", zerolog.DebugLevel},
		{"info", zerolog.InfoLevel},
		{"warn", zerolog.WarnLevel},
		{"error", zerolog.ErrorLevel},
		{"unknown", zerolog.InfoLevel},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			SetLevel(tc.input)
			assert.Equal(t, tc.expected, zerolog.GlobalLevel())
		})
	}
}

func TestInit(t *testing.T) {
	original := log.Logger
	origLevel := zerolog.GlobalLevel()
	t.Cleanup(func() {
		log.Logger = original
		zerolog.SetGlobalLevel(origLevel)
	})

	// Test console format
	Init("debug", "console")
	assert.Equal(t, zerolog.DebugLevel, zerolog.GlobalLevel())

	// Test non-console format
	Init("warn", "json")
	assert.Equal(t, zerolog.WarnLevel, zerolog.GlobalLevel())
}

func TestTraceIDHook(t *testing.T) {
	var buf bytes.Buffer
	hook := traceIDHook{}

	// 1. Without context in event (test Run directly with nil ctx)
	loggerWithoutCtx := zerolog.New(&buf).Level(zerolog.DebugLevel)
	event := loggerWithoutCtx.Info()
	hook.Run(event, zerolog.InfoLevel, "no ctx")
	event.Msg("done")
	assert.NotContains(t, buf.String(), "trace_id")

	// 2. With OpenTelemetry SpanContext
	tp := sdktrace.NewTracerProvider()
	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	buf.Reset()
	loggerWithCtx := zerolog.New(&buf).Level(zerolog.DebugLevel)
	eventWithCtx := loggerWithCtx.Info().Ctx(ctx)
	hook.Run(eventWithCtx, zerolog.InfoLevel, "msg")
	eventWithCtx.Msg("done")

	out := buf.String()
	t.Logf("output: %s", out)
	assert.Contains(t, out, span.SpanContext().TraceID().String())
	assert.Contains(t, out, span.SpanContext().SpanID().String())

	// 3. Context without trace
	ctxNoSpan := context.Background()
	buf.Reset()

	eventNoSpan := loggerWithCtx.Info().Ctx(ctxNoSpan)
	hook.Run(eventNoSpan, zerolog.InfoLevel, "msg")
	eventNoSpan.Msg("done")

	outNoSpan := buf.String()
	assert.NotContains(t, outNoSpan, "trace_id")
	assert.NotContains(t, outNoSpan, "span_id")
}
