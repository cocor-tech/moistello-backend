package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/infrastructure/ratelimit"
)

// errRedisUnavailable is returned when Redis cannot be reached during a rate
// limit check.
var errRedisUnavailable = errors.New("rate limiter: Redis unavailable")

var inMemoryLimiter = ratelimit.NewInMemoryRateLimiter()

// Rate-limit outage policy (#161).
//
// The repository historically had two rate limiters with opposite security
// postures: the legacy JS middleware/rateLimiter.js failed CLOSED (503 when
// Redis was unreachable), while this Go middleware failed OPEN (fell back to
// the in-memory limiter). The single policy, documented in
// docs/rate-limiting.md, is FAIL CLOSED by default: when Redis is down the
// limiter cannot see the counters it is supposed to enforce, so it refuses
// with 503 rather than silently letting every caller through. This matters
// most on the auth/OTP routes, where a limiter that stops limiting during an
// outage is worse than no limiter at all.
//
// Routes that genuinely cannot afford a 503 during a Redis outage (e.g.
// public read-only endpoints) can opt into fail-open per route with
// WithFailOpen(); security-critical routes can force fail-closed with
// WithFailClosed() even if the global config is flipped.
type rateLimitPolicy int

const (
	policyDefault    rateLimitPolicy = iota // use cfg.FailClosed
	policyFailClosed                        // always fail closed, ignore config
	policyFailOpen                          // always fail open, ignore config
)

type rateLimitOptions struct {
	policy rateLimitPolicy
	limits func() config.RateLimitConfig
}

type RateLimitOption func(*rateLimitOptions)

// WithFailClosed forces fail-closed behaviour for this route regardless of the
// global config — use it for auth/OTP and other security-critical paths.
func WithFailClosed() RateLimitOption {
	return func(o *rateLimitOptions) { o.policy = policyFailClosed }
}

// WithFailOpen forces fail-open behaviour (in-memory fallback) for this route,
// for paths where a 503 during a Redis outage is worse than an unenforced
// limit.
func WithFailOpen() RateLimitOption {
	return func(o *rateLimitOptions) { o.policy = policyFailOpen }
}

// WithLiveLimits makes the middleware read its Global/Authenticated/Auth limits
// from fn on every request instead of the value captured at startup, so they
// can be hot-reloaded. The outage policy is still taken from the startup config.
func WithLiveLimits(fn func() config.RateLimitConfig) RateLimitOption {
	return func(o *rateLimitOptions) { o.limits = fn }
}

// currentLimits returns the limits to enforce for this request.
func currentLimits(cfg config.RateLimitConfig, opts ...RateLimitOption) func() config.RateLimitConfig {
	o := rateLimitOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	if o.limits != nil {
		return o.limits
	}
	return func() config.RateLimitConfig { return cfg }
}

func resolveFailClosed(cfg config.RateLimitConfig, opts ...RateLimitOption) bool {
	o := rateLimitOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	switch o.policy {
	case policyFailClosed:
		return true
	case policyFailOpen:
		return false
	default:
		return cfg.FailClosed
	}
}

// enforceRateLimit runs the check and enforces the outage policy. It returns
// false when the request has been aborted (rate limited, or fail-closed during
// a Redis outage).
func enforceRateLimit(c *gin.Context, redisClient *redis.Client, key string, limit int, window time.Duration, failClosed bool, errorMessage string) bool {
	allowed, remaining, ttl, err := checkLimitWithWindow(c, redisClient, key, limit, window)
	if err != nil {
		if failClosed {
			log.Error().Err(err).Str("key", key).Msg("rate limit check failed: Redis unavailable, failing closed")
			setRateLimitHeaders(c, limit, 0, window)
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"success":    false,
				"error":      "service temporarily unavailable",
				"message":    "rate limiter unavailable — request refused",
				"retryAfter": int64(window.Seconds()),
			})
			return false
		}
		log.Warn().Err(err).Str("key", key).Msg("rate limit check failed: falling back to in-memory limiter")
		allowed, remaining, ttl, _ = inMemoryLimiter.Check(c.Request.Context(), key, limit, window)
	}

	setRateLimitHeaders(c, limit, remaining, ttl)

	if !allowed {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"success":    false,
			"error":      errorMessage,
			"retryAfter": int64(ttl.Seconds()),
		})
		return false
	}
	return true
}

func RateLimitMiddleware(redisClient *redis.Client, cfg config.RateLimitConfig, opts ...RateLimitOption) gin.HandlerFunc {
	failClosed := resolveFailClosed(cfg, opts...)
	limits := currentLimits(cfg, opts...)
	return func(c *gin.Context) {
		live := limits()
		key := "ratelimit:g:" + c.ClientIP()
		limit := live.Global
		if _, exists := c.Get("userID"); exists {
			key = "ratelimit:u:" + GetUserID(c)
			limit = live.Authenticated
		}

		if !enforceRateLimit(c, redisClient, key, limit, 1*time.Minute, failClosed, "rate limit exceeded") {
			return
		}
		c.Next()
	}
}

func AuthRateLimitMiddleware(redisClient *redis.Client, cfg config.RateLimitConfig, opts ...RateLimitOption) gin.HandlerFunc {
	// Auth/OTP is the case the policy exists for: fail closed even if the
	// global config is ever flipped, unless the caller explicitly overrides.
	failClosed := resolveFailClosed(cfg, append(opts, WithFailClosed())...)
	limits := currentLimits(cfg, opts...)
	return func(c *gin.Context) {
		key := "ratelimit:a:" + c.ClientIP()
		limit := limits().Auth

		if !enforceRateLimit(c, redisClient, key, limit, 1*time.Minute, failClosed, "too many auth attempts") {
			return
		}
		c.Next()
	}
}

func PerResourceRateLimitMiddleware(redisClient *redis.Client, resource string, limit int, window time.Duration, opts ...RateLimitOption) gin.HandlerFunc {
	failClosed := resolveFailClosed(config.RateLimitConfig{}, opts...)
	return func(c *gin.Context) {
		key := fmt.Sprintf("ratelimit:r:%s:%s", resource, c.ClientIP())

		if !enforceRateLimit(c, redisClient, key, limit, window, failClosed, fmt.Sprintf("rate limit exceeded for %s", resource)) {
			return
		}
		c.Next()
	}
}

// PasswordResetRateLimitMiddleware enforces dual-dimension rate limiting (per-IP and per-account) on password reset.
func PasswordResetRateLimitMiddleware(redisClient *redis.Client, ipLimit, accountLimit, windowSeconds int) gin.HandlerFunc {
	window := time.Duration(windowSeconds) * time.Second
	if ipLimit <= 0 {
		ipLimit = 5
	}
	if accountLimit <= 0 {
		accountLimit = 3
	}
	if windowSeconds <= 0 {
		window = 15 * time.Minute
	}

	return func(c *gin.Context) {
		ipKey := fmt.Sprintf("pwreset:ip:%s", c.ClientIP())
		if !enforceRateLimit(c, redisClient, ipKey, ipLimit, window, true, "password reset rate limit exceeded for this IP") {
			c.Header("Retry-After", strconv.FormatInt(int64(window.Seconds()), 10))
			return
		}

		account := c.Query("email")
		if account == "" {
			account = c.Query("account")
		}
		if account != "" {
			accountKey := fmt.Sprintf("pwreset:account:%s", account)
			if !enforceRateLimit(c, redisClient, accountKey, accountLimit, window, true, "password reset rate limit exceeded for this account") {
				c.Header("Retry-After", strconv.FormatInt(int64(window.Seconds()), 10))
				return
			}
		}

		c.Next()
	}
}

// checkLimitWithWindow implements a Redis-backed sliding window rate limiter
// using a sorted set with timestamps as scores. It returns (allowed, remaining, ttl, err).
// err is non-nil only when Redis is unreachable — the caller decides what that
// means per the rate-limit outage policy (fail closed by default, see
// docs/rate-limiting.md).
func checkLimitWithWindow(c *gin.Context, redisClient *redis.Client, key string, limit int, window time.Duration) (bool, int, time.Duration, error) {
	reqCtx := c.Request.Context()
	redisKey := fmt.Sprintf("ratelimit:%s", key)

	now := time.Now().UnixNano()
	windowStart := now - window.Nanoseconds()

	pipe := redisClient.Pipeline()
	pipe.ZRemRangeByScore(reqCtx, redisKey, "-inf", fmt.Sprintf("%d", windowStart))
	pipe.ZAdd(reqCtx, redisKey, redis.Z{Score: float64(now), Member: now})
	pipe.ZCard(reqCtx, redisKey)
	pipe.Expire(reqCtx, redisKey, window)

	results, err := pipe.Exec(reqCtx)
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("rate limit sliding window failed: Redis unreachable")
		return false, 0, 0, errRedisUnavailable
	}

	count := results[2].(*redis.IntCmd).Val()
	if count > int64(limit) {
		ttl, err := redisClient.TTL(reqCtx, redisKey).Result()
		if err != nil || ttl < 0 {
			ttl = window
		}
		return false, 0, ttl, nil
	}

	remaining := limit - int(count)
	return true, remaining, window, nil
}

func setRateLimitHeaders(c *gin.Context, limit, remaining int, ttl time.Duration) {
	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(ttl).Unix(), 10))
}
