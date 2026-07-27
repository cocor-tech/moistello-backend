package middleware

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type contextKey string

// RequestIDKey is the request-context key holding the correlation ID for the
// current request. Use GetRequestID to read it.
const RequestIDKey contextKey = "requestID"

// RequestIDGinKey is the gin.Context key holding the same correlation ID, for
// handlers that only have the *gin.Context at hand.
const RequestIDGinKey = "requestID"

// RequestIDHeader carries the correlation ID in and out of the service: an
// upstream proxy may supply one, and the response always echoes the ID used.
const RequestIDHeader = "X-Request-ID"

// maxRequestIDLen caps how much of a caller-supplied correlation ID we are
// willing to accept and repeat in every log line for the request.
const maxRequestIDLen = 128

func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(RequestIDKey).(string); ok {
		return id
	}
	return "unknown"
}

// LoggingMiddleware assigns every request a correlation ID and emits exactly one
// structured log line per request once the handler chain has completed. Fields
// are flat key/value pairs so log aggregators can index and query on them:
//
//	request_id, method, path, route, status, latency_ms, ip, bytes,
//	user_id (authenticated requests only), errors (handler errors only)
//
// The line is emitted at error level for 5xx responses, warn for 4xx and info
// otherwise, so alerting can key off severity rather than parsing status codes.
func LoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := resolveRequestID(c.GetHeader(RequestIDHeader))
		c.Set(RequestIDGinKey, requestID)
		c.Header(RequestIDHeader, requestID)

		ctx := context.WithValue(c.Request.Context(), RequestIDKey, requestID)
		c.Request = c.Request.WithContext(ctx)

		start := time.Now()
		c.Next()
		latency := time.Since(start)

		status := c.Writer.Status()
		event := log.WithLevel(levelForStatus(status)).
			Str("request_id", requestID).
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Int("status", status).
			Float64("latency_ms", float64(latency.Nanoseconds())/float64(time.Millisecond)).
			Str("ip", c.ClientIP())

		// FullPath is the matched route template ("/v1/circles/:id"), which keeps
		// log queries groupable even though path holds the concrete URL. It is
		// empty for unmatched routes.
		if route := c.FullPath(); route != "" {
			event = event.Str("route", route)
		}

		if size := c.Writer.Size(); size > 0 {
			event = event.Int("bytes", size)
		}

		// AuthMiddleware runs inside c.Next(), so the user ID is only readable
		// here, after the chain has completed.
		if userID := GetUserID(c); userID != "" {
			event = event.Str("user_id", userID)
		}

		if errs := strings.TrimSpace(c.Errors.ByType(gin.ErrorTypePrivate).String()); errs != "" {
			event = event.Str("errors", errs)
		}

		event.Msg("request completed")
	}
}

// levelForStatus maps a response status onto a log level: server faults are
// errors, client faults are warnings, everything else is routine.
func levelForStatus(status int) zerolog.Level {
	switch {
	case status >= 500:
		return zerolog.ErrorLevel
	case status >= 400:
		return zerolog.WarnLevel
	default:
		return zerolog.InfoLevel
	}
}

// resolveRequestID reuses an upstream correlation ID when the caller supplied a
// sane one, and mints a fresh UUID otherwise. Oversized or non-printable values
// are rejected rather than repeated into every log line for the request.
func resolveRequestID(header string) string {
	id := strings.TrimSpace(header)
	if id == "" || len(id) > maxRequestIDLen || !isPrintableASCII(id) {
		return uuid.New().String()
	}
	return id
}

func isPrintableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
