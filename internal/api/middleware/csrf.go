package middleware

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// CSRFTokenValidator middleware checks that state-changing requests and
// WebSocket upgrades include a valid X-CSRF-Token header. The token is an
// independent cryptographically random value generated server-side and stored
// in Redis, bound to the user's access token. It is not derivable from the
// JWT itself.
func CSRFTokenValidator(redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		isWebSocketUpgrade := strings.EqualFold(c.GetHeader("Upgrade"), "websocket")
		if (method == "GET" || method == "HEAD" || method == "OPTIONS") && !isWebSocketUpgrade {
			c.Next()
			return
		}

		requestToken := c.GetHeader("X-CSRF-Token")
		if requestToken == "" {
			log.Warn().Str("path", c.Request.URL.Path).Str("method", method).Msg("missing CSRF token")
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "missing CSRF token",
			})
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": "missing authorization",
			})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" || len(parts[1]) < 32 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error": "invalid authorization format",
			})
			return
		}

		token := parts[1]
		tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
		ctx := c.Request.Context()

		expectedToken, err := redisClient.Get(ctx, "csrf:"+tokenHash).Result()
		if err == redis.Nil {
			log.Warn().Str("path", c.Request.URL.Path).Msg("CSRF token not found — session may have been revoked")
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "invalid CSRF token",
			})
			return
		}
		if err != nil {
			log.Error().Err(err).Str("path", c.Request.URL.Path).Msg("Redis CSRF lookup failed")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"error":   "authentication service unavailable",
			})
			return
		}

		if !compareTokens(requestToken, expectedToken) {
			log.Warn().Str("path", c.Request.URL.Path).Msg("CSRF token mismatch")
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "invalid CSRF token",
			})
			return
		}

		c.Next()
	}
}

// compareTokens performs a constant-time comparison of two tokens to prevent
// timing attacks.
func compareTokens(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
