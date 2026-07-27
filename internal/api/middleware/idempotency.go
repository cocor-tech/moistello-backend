package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	defaultIdempotencyTTL = 24 * time.Hour
)

// IdempotencyMiddleware prevents race conditions and request replay by checking
// for Idempotency-Key or X-Idempotency-Key request headers. It uses an atomic
// Redis SET NX operation with TTL. If a key is currently processing or has already
// been used, concurrent/duplicate requests are rejected with 409 Conflict.
func IdempotencyMiddleware(redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
		if key == "" {
			key = strings.TrimSpace(c.GetHeader("X-Idempotency-Key"))
		}

		if key == "" {
			c.Next()
			return
		}

		redisKey := "idempotency:" + key
		ctx := c.Request.Context()

		// Atomic SET NX with TTL
		set, err := redisClient.SetNX(ctx, redisKey, "processing", defaultIdempotencyTTL).Result()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"error":   "service unavailable: idempotency store error",
			})
			return
		}

		if !set {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{
				"success": false,
				"error":   "idempotency key already used or request in progress",
			})
			return
		}

		c.Next()
	}
}
