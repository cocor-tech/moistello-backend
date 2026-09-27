package integration_test

// Regression coverage for #406.
//
// docs/rate-limiting.md documents a single outage policy: when Redis is
// unreachable, the rate limiter fails CLOSED (503) by default, for every
// route class including auth/OTP. internal/api/middleware/rate_limit_test.go
// already proves the middleware constructors behave this way in isolation —
// but that file lives in package middleware_test, which CI's unit-test job
// covers (`go test ./... -race -count=1 -short`), not the integration-test
// job (`go test ./tests/integration/... -count=1`). #406 specifically asks
// for this proof to run in the integration job, against the same route
// wiring (middleware + method + path) the real router actually uses for
// auth/OTP, not just the middleware constructor called directly on a
// synthetic test route.
//
// newAuthGroupRouter mirrors the auth-group wiring from
// internal/api/router.go: the /v1/auth group is wrapped in
// AuthRateLimitMiddleware, and register/register-verify additionally layer
// PerResourceRateLimitMiddleware with the "otp" resource and
// WithFailClosed() — exactly as router.go constructs them via its
// perResource() helper.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api/middleware"
)

// unreachableRedis returns a real go-redis client pointed at an address
// nothing listens on, so every command fails as it would during an actual
// Redis outage — not a mock, the same failure mode production would see.
func unreachableRedis() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:19999", DB: 0})
}

// newAuthGroupRouter reproduces the /v1/auth route group's middleware
// wiring from internal/api/router.go: AuthRateLimitMiddleware on the group,
// plus PerResourceRateLimitMiddleware("otp", WithFailClosed()) layered on
// register/register-verify specifically. It also mounts the global tier
// exactly as router.go applies it ahead of /health*, for the companion
// "safe GET" test in ratelimit_failclosed_policy_test.go.
func newAuthGroupRouter(rdb *redis.Client, cfg config.RateLimitConfig) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	ok := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }

	api := r.Group("/v1")
	auth := api.Group("/auth")
	auth.Use(middleware.AuthRateLimitMiddleware(rdb, cfg))
	{
		auth.POST("/register",
			middleware.PerResourceRateLimitMiddleware(rdb, "otp", cfg.OTPLimit, time.Duration(cfg.OTPWindowSeconds)*time.Second, middleware.WithFailClosed()),
			ok,
		)
		auth.POST("/register/verify",
			middleware.PerResourceRateLimitMiddleware(rdb, "otp", cfg.OTPLimit, time.Duration(cfg.OTPWindowSeconds)*time.Second, middleware.WithFailClosed()),
			ok,
		)
		auth.POST("/nonce", ok)
		auth.POST("/verify", ok)
		auth.POST("/refresh", ok)
	}

	// The global tier, exactly as router.go applies it ahead of /health*:
	// r.Use(middleware.RateLimitMiddleware(...)) before registering the
	// health routes, with no per-route override — so today, a "safe" GET
	// like /health is on the same fail-closed-by-default policy as
	// everything else, per docs/rate-limiting.md's "every route class"
	// language. There is currently no route anywhere in router.go that
	// opts into WithFailOpen().
	r.Use(middleware.RateLimitMiddleware(rdb, cfg))
	r.GET("/health", ok)

	return r
}

func testRateLimitConfig() config.RateLimitConfig {
	return config.RateLimitConfig{
		Global:           100,
		Authenticated:    300,
		Auth:             10,
		FailClosed:       true,
		OTPLimit:         5,
		OTPWindowSeconds: 900,
	}
}

// TestAuthAndOTPRoutes_FailClosed503_WhenRedisDown is the direct claim in
// #406: with Redis unreachable, every auth/OTP route returns 503 with the
// documented error envelope — never a 200 (fail-open) and never a panic/500
// (crash).
func TestAuthAndOTPRoutes_FailClosed503_WhenRedisDown(t *testing.T) {
	rdb := unreachableRedis()
	defer rdb.Close()

	r := newAuthGroupRouter(rdb, testRateLimitConfig())

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"register (OTP)", http.MethodPost, "/v1/auth/register"},
		{"register/verify (OTP)", http.MethodPost, "/v1/auth/register/verify"},
		{"nonce", http.MethodPost, "/v1/auth/nonce"},
		{"verify", http.MethodPost, "/v1/auth/verify"},
		{"refresh", http.MethodPost, "/v1/auth/refresh"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(tc.method, tc.path, nil)
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusServiceUnavailable, w.Code, "%s must fail closed (503), not fail open or crash", tc.path)
			assert.Contains(t, w.Body.String(), "service temporarily unavailable")
			assert.Contains(t, w.Body.String(), "rate limiter unavailable")
		})
	}
}
