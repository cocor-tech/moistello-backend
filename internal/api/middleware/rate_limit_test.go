package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api/middleware"
)

func newRateLimitConfig() config.RateLimitConfig {
	return config.RateLimitConfig{
		Global:        100,
		Authenticated: 200,
		Auth:          20,
		FailClosed:    true,
	}
}

// newUnreachableRedis returns a Redis client that will always fail (no server at
// that address), used to exercise the Redis-down behaviour.
func newUnreachableRedis() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "localhost:19999", DB: 0})
}

// newMiniredis returns a Redis client backed by an in-process Redis server, used
// to exercise the Redis-up path deterministically without a live daemon.
func newMiniredis(t *testing.T) (*redis.Client, func()) {
	t.Helper()
	s, err := miniredis.Run()
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: s.Addr(), DB: 0})
	return rdb, func() {
		rdb.Close()
		s.Close()
	}
}

// ── Redis down ───────────────────────────────────────────────────────────

// TestRateLimitMiddleware_FailsClosedWhenRedisDown verifies the single policy
// (#161): by default the middleware fails CLOSED (503) when Redis is
// unreachable — matching the legacy JS middleware/rateLimiter.js, which always
// returned 503 on a Redis outage.
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
	assert.Contains(t, w.Body.String(), "service temporarily unavailable")
	// Rate-limit headers are still emitted so clients can observe the state.
	assert.Equal(t, "100", w.Header().Get("X-RateLimit-Limit"))
}

// TestRateLimitMiddleware_FailsClosedWhenRedisDownEvenIfConfigFlips verifies
// that a route can force fail-closed regardless of the global config.
func TestRateLimitMiddleware_FailsClosedWhenRedisDownEvenIfConfigFlips(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newUnreachableRedis()
	defer rdb.Close()

	cfg := newRateLimitConfig()
	cfg.FailClosed = false // operator flipped the global default to fail-open
	r := gin.New()
	r.Use(middleware.RateLimitMiddleware(rdb, cfg, middleware.WithFailClosed()))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestRateLimitMiddleware_FailOpenWhenRedisDown verifies the opt-in fail-open
// route: with WithFailOpen the middleware falls back to the in-memory limiter
// and lets the request through.
func TestRateLimitMiddleware_FailOpenWhenRedisDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newUnreachableRedis()
	defer rdb.Close()

	cfg := newRateLimitConfig()
	r := gin.New()
	r.Use(middleware.RateLimitMiddleware(rdb, cfg, middleware.WithFailOpen()))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "100", w.Header().Get("X-RateLimit-Limit"))
}

// TestAuthRateLimitMiddleware_FailsClosedWhenRedisDown verifies the auth/OTP
// limiter fails closed on a Redis outage — the case the policy exists for.
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
	assert.Contains(t, w.Body.String(), "service temporarily unavailable")
}

// TestAuthRateLimitMiddleware_FailsClosedWhenRedisDownEvenIfConfigFlips
// verifies that the auth limiter stays fail-closed even when the global config
// is flipped to fail-open — auth/OTP must never silently stop limiting.
func TestAuthRateLimitMiddleware_FailsClosedWhenRedisDownEvenIfConfigFlips(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newUnreachableRedis()
	defer rdb.Close()

	cfg := newRateLimitConfig()
	cfg.FailClosed = false
	r := gin.New()
	r.Use(middleware.AuthRateLimitMiddleware(rdb, cfg))
	r.POST("/auth/login", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/login", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ── Redis up ─────────────────────────────────────────────────────────────

func TestRateLimitMiddleware_AllowedWhenRedisUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb, cleanup := newMiniredis(t)
	defer cleanup()

	cfg := newRateLimitConfig()
	r := gin.New()
	r.Use(middleware.RateLimitMiddleware(rdb, cfg))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "100", w.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "99", w.Header().Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, w.Header().Get("X-RateLimit-Reset"))
}

func TestRateLimitMiddleware_ExceedsLimitWhenRedisUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb, cleanup := newMiniredis(t)
	defer cleanup()

	cfg := config.RateLimitConfig{Global: 2, Authenticated: 200, Auth: 20, FailClosed: true}
	r := gin.New()
	r.Use(middleware.RateLimitMiddleware(rdb, cfg))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "requests 1-2 should be allowed")
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), "rate limit exceeded")
}

func TestAuthRateLimitMiddleware_AllowedWhenRedisUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb, cleanup := newMiniredis(t)
	defer cleanup()

	cfg := newRateLimitConfig()
	r := gin.New()
	r.Use(middleware.AuthRateLimitMiddleware(rdb, cfg))
	r.POST("/auth/login", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/login", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "20", w.Header().Get("X-RateLimit-Limit"))
}

func TestRateLimitMiddleware_AuthenticatedUserLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb, cleanup := newMiniredis(t)
	defer cleanup()

	cfg := newRateLimitConfig()
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

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "200", w.Header().Get("X-RateLimit-Limit"))
}

func TestRateLimitMiddleware_MultipleMiddlewareChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb, cleanup := newMiniredis(t)
	defer cleanup()

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

	assert.Equal(t, http.StatusOK, w.Code)
}

// ── PerResourceRateLimitMiddleware (#197) ───────────────────────────────────
//
// The middleware existed but was never applied to a route; these tests cover
// it directly, matching the coverage the group-level limiters above already
// have (allow within limit, reject over limit, fail closed on a Redis
// outage, and — the actual point of "per resource" — that two different
// resources on the same client IP get independent budgets).

func TestPerResourceRateLimitMiddleware_AllowedWithinLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb, cleanup := newMiniredis(t)
	defer cleanup()

	r := gin.New()
	r.POST("/swap/offer", middleware.PerResourceRateLimitMiddleware(rdb, "swap", 2, time.Minute), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/swap/offer", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "2", w.Header().Get("X-RateLimit-Limit"))
}

func TestPerResourceRateLimitMiddleware_ExceedsLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb, cleanup := newMiniredis(t)
	defer cleanup()

	r := gin.New()
	r.POST("/swap/offer", middleware.PerResourceRateLimitMiddleware(rdb, "swap", 2, time.Minute), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/swap/offer", nil)
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "requests 1-2 should be allowed")
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/swap/offer", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), "rate limit exceeded for swap")
}

func TestPerResourceRateLimitMiddleware_FailsClosedWhenRedisDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb := newUnreachableRedis()
	defer rdb.Close()

	r := gin.New()
	r.POST("/auth/register", middleware.PerResourceRateLimitMiddleware(rdb, "otp", 5, 15*time.Minute, middleware.WithFailClosed()), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/register", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestPerResourceRateLimitMiddleware_IndependentBudgetsPerResource is the
// core "per resource" guarantee: a client that has exhausted the "swap"
// budget can still make a "contribute" call, because each resource key is
// scoped by name (see the "ratelimit:r:<resource>:<ip>" key format in
// PerResourceRateLimitMiddleware) — a single shared bucket would incorrectly
// let one endpoint's traffic starve an unrelated one's budget.
func TestPerResourceRateLimitMiddleware_IndependentBudgetsPerResource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb, cleanup := newMiniredis(t)
	defer cleanup()

	r := gin.New()
	r.POST("/swap/offer", middleware.PerResourceRateLimitMiddleware(rdb, "swap", 1, time.Minute), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})
	r.POST("/circles/1/contribute", middleware.PerResourceRateLimitMiddleware(rdb, "contribute", 1, time.Minute), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	// Exhaust the "swap" budget.
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/swap/offer", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/swap/offer", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "swap budget should now be exhausted")

	// "contribute" from the same client IP is unaffected.
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/circles/1/contribute", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "contribute has its own independent budget")
}
