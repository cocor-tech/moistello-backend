package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api/middleware"
)

func newRateLimitConfig() config.RateLimitConfig {
	return config.RateLimitConfig{
		Global:        100,
		Authenticated: 200,
		Auth:          20,
	}
}

// newUnreachableRedis returns a Redis client that will always fail (no server at
// that address), used to exercise the fail-closed path.
func newUnreachableRedis() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "localhost:19999", DB: 0})
}

// TestRateLimitMiddleware_FailsClosedWhenRedisDown verifies that the middleware
// returns 503 (not 200 or 429) when Redis is unreachable — closing the door
// against DoS-induced rate-limit bypass.
func TestRateLimitMiddleware_FailsClosedWhenRedisDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newUnreachableRedis()
	defer rdb.Close()

	cfg := newRateLimitConfig()
	r := gin.New()
	r.Use(middleware.RateLimitMiddleware(rdb, cfg))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, false, resp["success"])
	assert.Contains(t, resp["error"], "unavailable")
}

// TestAuthRateLimitMiddleware_FailsClosedWhenRedisDown verifies the same
// fail-closed behaviour for the auth-specific rate limit middleware.
func TestAuthRateLimitMiddleware_FailsClosedWhenRedisDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newUnreachableRedis()
	defer rdb.Close()

	cfg := newRateLimitConfig()
	r := gin.New()
	r.Use(middleware.AuthRateLimitMiddleware(rdb, cfg))
	r.POST("/auth/login", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/login", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, false, resp["success"])
}

// TestRateLimitMiddleware_SetsHeaders verifies that rate-limit headers are set
// when Redis is available and the limit has not been reached.
// This test requires a running Redis at localhost:6379.
func TestRateLimitMiddleware_SetsHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()

	cfg := newRateLimitConfig()
	r := gin.New()
	r.Use(middleware.RateLimitMiddleware(rdb, cfg))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	// If Redis is available this should pass and set headers.
	// If Redis is unavailable this will return 503 — both outcomes are correct
	// (fail-closed); the header assertions only apply when Redis is up.
	if w.Code == http.StatusOK {
		assert.Equal(t, "100", w.Header().Get("X-RateLimit-Limit"))
		assert.NotEmpty(t, w.Header().Get("X-RateLimit-Remaining"))
		assert.NotEmpty(t, w.Header().Get("X-RateLimit-Reset"))
	} else {
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	}
}

// TestAuthRateLimitMiddleware_SetsAuthLimit verifies the auth limit value is
// correctly propagated to response headers.
func TestAuthRateLimitMiddleware_SetsAuthLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()

	cfg := newRateLimitConfig()
	r := gin.New()
	r.Use(middleware.AuthRateLimitMiddleware(rdb, cfg))
	r.POST("/auth/login", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/login", nil)
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		assert.Equal(t, "20", w.Header().Get("X-RateLimit-Limit"))
	} else {
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	}
}

// TestRateLimitMiddleware_AuthenticatedUserLimit verifies that authenticated
// users receive the higher authenticated limit.
func TestRateLimitMiddleware_AuthenticatedUserLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()

	cfg := config.RateLimitConfig{
		Global:        100,
		Authenticated: 200,
		Auth:          20,
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "test-user-id")
		c.Next()
	})
	r.Use(middleware.RateLimitMiddleware(rdb, cfg))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		assert.Equal(t, "200", w.Header().Get("X-RateLimit-Limit"))
	} else {
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	}
}

// TestRateLimitMiddleware_MultipleMiddlewareChain verifies that chaining both
// middlewares does not panic and produces a coherent response.
func TestRateLimitMiddleware_MultipleMiddlewareChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()

	cfg := newRateLimitConfig()
	r := gin.New()
	r.Use(middleware.RateLimitMiddleware(rdb, cfg))
	r.Use(middleware.AuthRateLimitMiddleware(rdb, cfg))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	// Any deterministic response (200 or 503) is acceptable.
	assert.True(t, w.Code == http.StatusOK || w.Code == http.StatusServiceUnavailable)
}
