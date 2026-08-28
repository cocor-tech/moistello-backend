package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/pkg/logger"
	"github.com/moistello/backend/pkg/response"
	"github.com/rs/zerolog/log"
)

type Claims struct {
	jwt.RegisteredClaims
	UserID string `json:"sub"`
	Wallet string `json:"wallet"`
	Role   string `json:"role"`
}

func AuthMiddleware(publicKeyPEM []byte) gin.HandlerFunc {
	publicKey, method, err := auth.ParsePublicVerifyingKey(publicKeyPEM)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to parse JWT public key")
	}

	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Abort()
			response.Unauthorized(c, "missing authorization header")
			return
		}
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.Abort()
			response.Unauthorized(c, "invalid authorization format")
			return
		}
		token, err := jwt.ParseWithClaims(parts[1], &Claims{}, func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != method.Alg() {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return publicKey, nil
		}, jwt.WithValidMethods([]string{method.Alg()}))
		if err != nil || !token.Valid {
			c.Abort()
			response.Unauthorized(c, "invalid or expired token")
			return
		}
		claims, ok := token.Claims.(*Claims)
		if !ok {
			c.Abort()
			response.Unauthorized(c, "invalid token claims")
			return
		}
		c.Set("userID", claims.UserID)
		c.Set("wallet", claims.Wallet)
		c.Set("role", claims.Role)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), logger.UserIDKey, claims.UserID))
		log.Debug().Str("userID", claims.UserID).Str("path", c.Request.URL.Path).Msg("authenticated request")
		c.Next()
	}
}

func OptionalAuthMiddleware(publicKeyPEM []byte) gin.HandlerFunc {
	publicKey, method, err := auth.ParsePublicVerifyingKey(publicKeyPEM)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to parse JWT public key")
	}

	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.Next()
			return
		}
		token, err := jwt.ParseWithClaims(parts[1], &Claims{}, func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != method.Alg() {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return publicKey, nil
		}, jwt.WithValidMethods([]string{method.Alg()}))
		if err != nil || !token.Valid {
			c.Next()
			return
		}
		claims, ok := token.Claims.(*Claims)
		if !ok {
			c.Next()
			return
		}
		c.Set("userID", claims.UserID)
		c.Set("wallet", claims.Wallet)
		c.Set("role", claims.Role)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), logger.UserIDKey, claims.UserID))
		c.Next()
	}
}

func AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("role")
		if role != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "error": "admin access required"})
			return
		}
		c.Next()
	}
}

func GetUserID(c *gin.Context) string {
	raw, exists := c.Get("userID")
	if !exists {
		return ""
	}
	id, ok := raw.(string)
	if !ok {
		return ""
	}
	return id
}

func GetWallet(c *gin.Context) string {
	raw, exists := c.Get("wallet")
	if !exists {
		return ""
	}
	w, ok := raw.(string)
	if !ok {
		return ""
	}
	return w
}

func GetRole(c *gin.Context) string {
	raw, exists := c.Get("role")
	if !exists {
		return ""
	}
	r, ok := raw.(string)
	if !ok {
		return ""
	}
	return r
}

// AdminAPIKeyMiddleware validates the X-Admin-API-Key header against the
// configured admin API key. Used to protect internal endpoints like /metrics.
func AdminAPIKeyMiddleware(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if apiKey == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "admin API key not configured",
			})
			return
		}
		if c.GetHeader("X-Admin-API-Key") != apiKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "invalid admin API key",
			})
			return
		}
		c.Next()
	}
}
