package middleware

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/moistello/backend/pkg/logger"
)

func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(logger.RequestIDKey).(string); ok {
		return id
	}
	return "unknown"
}

func GetTraceID(ctx context.Context) string {
	spanCtx := trace.SpanFromContext(ctx).SpanContext()
	if spanCtx.HasTraceID() {
		return spanCtx.TraceID().String()
	}
	return ""
}

func LoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = c.GetHeader("X-Request-Id")
		}
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Set("requestID", requestID)
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)

		ctx := context.WithValue(c.Request.Context(), logger.RequestIDKey, requestID)
		c.Request = c.Request.WithContext(ctx)

		// Correlate the OpenTelemetry span with the request so the trace shows
		// which logical request produced each span (#229).
		if span := trace.SpanFromContext(ctx); span.IsRecording() {
			span.SetAttributes(attribute.String("request.id", requestID))
		}

		start := time.Now()
		c.Next()
		duration := time.Since(start)
		status := c.Writer.Status()

		log.Info().
			Str("requestID", requestID).
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Int("status", status).
			Dur("duration", duration).
			Str("ip", c.ClientIP()).
			Str("userAgent", c.Request.UserAgent()).
			Str("trace_id", GetTraceID(ctx)).
			Str("callerIdentity", GetUserIDFromGin(c)).
			Msg("request completed")
	}
}

func GetUserIDFromGin(c *gin.Context) string {
	if raw, exists := c.Get("userID"); exists {
		if id, ok := raw.(string); ok {
			return id
		}
	}
	return "anonymous"
}
