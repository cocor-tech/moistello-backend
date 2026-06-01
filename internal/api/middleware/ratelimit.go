package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/config"
)

func RateLimitMiddleware(redisClient *redis.Client, cfg config.RateLimitConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := "ratelimit:g:" + c.ClientIP()
		limit := cfg.Global
		if _, exists := c.Get("userID"); exists {
			key = "ratelimit:u:" + GetUserID(c)
			limit = cfg.Authenticated
		}

		allowed, remaining, ttl := checkLimit(c, redisClient, key, limit)
		setRateLimitHeaders(c, limit, remaining, ttl)

		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success":    false,
				"error":      "rate limit exceeded",
				"retryAfter": ttl.Seconds(),
			})
			return
		}
		c.Next()
	}
}

func AuthRateLimitMiddleware(redisClient *redis.Client, cfg config.RateLimitConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := "ratelimit:a:" + c.ClientIP()
		limit := cfg.Auth

		allowed, remaining, ttl := checkLimit(c, redisClient, key, limit)
		setRateLimitHeaders(c, limit, remaining, ttl)

		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success":    false,
				"error":      "too many auth attempts",
				"retryAfter": ttl.Seconds(),
			})
			return
		}
		c.Next()
	}
}

func PerResourceRateLimitMiddleware(redisClient *redis.Client, resource string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := fmt.Sprintf("ratelimit:r:%s:%s", resource, c.ClientIP())
		allowed, remaining, ttl := checkLimitWithWindow(c, redisClient, key, limit, window)
		setRateLimitHeaders(c, limit, remaining, ttl)

		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success":    false,
				"error":      fmt.Sprintf("rate limit exceeded for %s", resource),
				"retryAfter": ttl.Seconds(),
			})
			return
		}
		c.Next()
	}
}

func checkLimit(c *gin.Context, redisClient *redis.Client, key string, limit int) (bool, int, time.Duration) {
	return checkLimitWithWindow(c, redisClient, key, limit, 1*time.Minute)
}

func checkLimitWithWindow(c *gin.Context, redisClient *redis.Client, key string, limit int, window time.Duration) (bool, int, time.Duration) {
	reqCtx := c.Request.Context()

	current, err := redisClient.Get(reqCtx, key).Int()
	if err != nil && err != redis.Nil {
		log.Warn().Err(err).Str("key", key).Msg("rate limit check failed, allowing")
		return true, limit, 0
	}

	remaining := limit - current - 1
	if remaining < 0 {
		remaining = 0
	}

	if current >= limit {
		ttl, err := redisClient.TTL(reqCtx, key).Result()
		if err != nil || ttl < 0 {
			ttl = window
		}
		return false, 0, ttl
	}

	pipe := redisClient.Pipeline()
	pipe.Incr(reqCtx, key)
	pipe.Expire(reqCtx, key, window)
	if _, err := pipe.Exec(reqCtx); err != nil {
		log.Warn().Err(err).Str("key", key).Msg("rate limit pipeline failed, allowing")
		return true, limit, 0
	}

	return true, remaining, window
}

func setRateLimitHeaders(c *gin.Context, limit, remaining int, ttl time.Duration) {
	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(ttl).Unix(), 10))
}
