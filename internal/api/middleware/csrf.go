package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// CSRFTokenValidator middleware checks that state-changing requests and
// WebSocket upgrades include a valid X-CSRF-Token header. The token is the last
// 32 characters of the user's Bearer token, which the frontend derives easily.
//
// This provides CSRF protection because:
//   - The auth token is stored in a SameSite=Lax cookie
//   - A third-party site cannot read the auth token from cookies
//   - Without the auth token, an attacker cannot forge the CSRF token
func CSRFTokenValidator() gin.HandlerFunc {
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
				"error": "missing CSRF token",
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
		expected := token[len(token)-32:]

		if requestToken != expected {
			log.Warn().Str("path", c.Request.URL.Path).Msg("CSRF token mismatch")
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"error": "invalid CSRF token",
			})
			return
		}

		c.Next()
	}
}
