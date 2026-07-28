package logger

import (
	"context"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/trace"
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

func Init(level string, format string) {
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
	if format == "console" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
	}
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	// Add trace ID hook to include OpenTelemetry trace context in logs
	log.Logger = log.Hook(traceIDHook{})
}
