package logger

import (
	"context"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/trace"
)

// ContextKey is the type used for request-scoped context keys so they cannot
// collide with keys from other packages. Values stored under these keys are
// picked up by Ctx and included in every structured log line emitted during a
// request, tying logs back to the originating request.
type ContextKey string

const (
	// RequestIDKey is the context key for the request correlation ID.
	RequestIDKey ContextKey = "requestID"
	// UserIDKey is the context key for the authenticated user ID.
	UserIDKey ContextKey = "userID"
)

// traceIDHook is a zerolog hook that adds trace ID from OpenTelemetry context
type traceIDHook struct{}

func (h traceIDHook) Run(e *zerolog.Event, level zerolog.Level, msg string) {
	ctx := e.GetCtx()
	if ctx == nil {
		return
	}

	spanCtx := trace.SpanFromContext(ctx).SpanContext()
	if spanCtx.HasTraceID() {
		e.Str("trace_id", spanCtx.TraceID().String())
		if spanCtx.HasSpanID() {
			e.Str("span_id", spanCtx.SpanID().String())
		}
	}
}

// Ctx extracts a zerolog.Logger from context, enriched with request-scoped fields.
func Ctx(ctx context.Context) zerolog.Logger {
	if ctx == nil {
		return log.Logger
	}
	logger := log.Logger.With()
	if reqID, ok := ctx.Value(RequestIDKey).(string); ok && reqID != "" {
		logger = logger.Str("requestID", reqID)
	}
	if userID, ok := ctx.Value(UserIDKey).(string); ok && userID != "" {
		logger = logger.Str("userID", userID)
	}
	return logger.Logger()
}

// SetLevel changes the global log level; unknown values fall back to info.
func SetLevel(level string) {
	switch level {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

func Init(level string, format string) {
	SetLevel(level)
	if format == "console" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
	}
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	// Add trace ID hook to include OpenTelemetry trace context in logs
	log.Logger = log.Hook(traceIDHook{})
}
