package middleware_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/middleware"
)

// captureLog runs fn with the global zerolog logger writing JSON into a buffer,
// then decodes the single log line it produced.
func captureLog(t *testing.T, fn func()) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	original := zlog.Logger
	originalLevel := zerolog.GlobalLevel()
	zlog.Logger = zerolog.New(&buf).With().Timestamp().Logger()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	t.Cleanup(func() {
		zlog.Logger = original
		zerolog.SetGlobalLevel(originalLevel)
	})

	fn()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 1, "middleware should emit exactly one log line per request")

	var fields map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &fields), "log line must be valid JSON")
	return fields
}

func newLoggingRouter(handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.LoggingMiddleware())
	r.GET("/v1/circles/:id", handler)
	return r
}

func doRequest(r *gin.Engine, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLoggingMiddleware_EmitsStructuredFields(t *testing.T) {
	r := newLoggingRouter(func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	var rec *httptest.ResponseRecorder
	fields := captureLog(t, func() {
		rec = doRequest(r, "/v1/circles/abc", nil)
	})

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "request completed", fields["message"])
	assert.Equal(t, "info", fields["level"])
	assert.Equal(t, http.MethodGet, fields["method"])
	assert.Equal(t, "/v1/circles/abc", fields["path"])
	assert.Equal(t, "/v1/circles/:id", fields["route"])
	assert.Equal(t, float64(http.StatusOK), fields["status"])
	assert.NotEmpty(t, fields["request_id"])
	assert.NotEmpty(t, fields["ip"])

	latency, ok := fields["latency_ms"].(float64)
	require.True(t, ok, "latency_ms must be a number, got %T", fields["latency_ms"])
	assert.GreaterOrEqual(t, latency, 0.0)

	// Unauthenticated requests carry no user, so the field is omitted entirely.
	assert.NotContains(t, fields, "user_id")
}

func TestLoggingMiddleware_LevelFollowsStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		level  string
	}{
		{"success is info", http.StatusOK, "info"},
		{"redirect is info", http.StatusFound, "info"},
		{"client error is warn", http.StatusBadRequest, "warn"},
		{"not found is warn", http.StatusNotFound, "warn"},
		{"server error is error", http.StatusInternalServerError, "error"},
		{"gateway error is error", http.StatusBadGateway, "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newLoggingRouter(func(c *gin.Context) {
				c.JSON(tt.status, gin.H{"status": tt.status})
			})

			fields := captureLog(t, func() {
				doRequest(r, "/v1/circles/abc", nil)
			})

			assert.Equal(t, tt.level, fields["level"])
			assert.Equal(t, float64(tt.status), fields["status"])
		})
	}
}

func TestLoggingMiddleware_IncludesUserIDWhenAuthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.LoggingMiddleware())
	// Stands in for AuthMiddleware, which also runs after LoggingMiddleware and
	// populates the same gin context key.
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-123")
		c.Next()
	})
	r.GET("/v1/circles/:id", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	fields := captureLog(t, func() {
		doRequest(r, "/v1/circles/abc", nil)
	})

	assert.Equal(t, "user-123", fields["user_id"])
}

func TestLoggingMiddleware_ReusesUpstreamRequestID(t *testing.T) {
	r := newLoggingRouter(func(c *gin.Context) {
		// Handlers read the same ID off the request context for downstream calls.
		assert.Equal(t, "trace-from-proxy", middleware.GetRequestID(c.Request.Context()))
		c.Status(http.StatusOK)
	})

	var rec *httptest.ResponseRecorder
	fields := captureLog(t, func() {
		rec = doRequest(r, "/v1/circles/abc", map[string]string{"X-Request-ID": "trace-from-proxy"})
	})

	assert.Equal(t, "trace-from-proxy", fields["request_id"])
	assert.Equal(t, "trace-from-proxy", rec.Header().Get("X-Request-ID"))
}

func TestLoggingMiddleware_GeneratesRequestIDWhenAbsentOrUnusable(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{"absent", ""},
		{"blank", "   "},
		{"oversized", strings.Repeat("a", 129)},
		{"control characters", "trace\nid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newLoggingRouter(func(c *gin.Context) { c.Status(http.StatusOK) })

			var rec *httptest.ResponseRecorder
			fields := captureLog(t, func() {
				headers := map[string]string{}
				if tt.header != "" {
					headers["X-Request-ID"] = tt.header
				}
				rec = doRequest(r, "/v1/circles/abc", headers)
			})

			requestID, ok := fields["request_id"].(string)
			require.True(t, ok)
			assert.NotEqual(t, tt.header, requestID)
			// A generated ID is a UUIDv4, and the response echoes what was logged.
			assert.Len(t, requestID, 36)
			assert.Equal(t, requestID, rec.Header().Get("X-Request-ID"))
		})
	}
}
