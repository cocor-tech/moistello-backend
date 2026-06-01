package middleware

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

func TokenBlocklistMiddleware(redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
			c.Next()
			return
		}
		token := parts[1]

		tokenHash := sha256.Sum256([]byte(token))
		blocklistKey := fmt.Sprintf("token:blocklist:%x", tokenHash[:])

		ctx := c.Request.Context()
		exists, err := redisClient.Exists(ctx, blocklistKey).Result()
		if err != nil {
			log.Error().Err(err).Msg("Redis blocklist check failed — denying request")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"error":   "Authentication service unavailable",
			})
			return
		}

		if exists > 0 {
			log.Warn().
				Str("tokenHash", fmt.Sprintf("%x", tokenHash[:4])).
				Msg("Blocklisted token used — possible replay attack")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "Token has been revoked",
			})
			return
		}

		c.Next()
	}
}

func BlocklistToken(ctx context.Context, redisClient *redis.Client, token string, expiresAt time.Time) {
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return
	}

	tokenHash := sha256.Sum256([]byte(token))
	blocklistKey := fmt.Sprintf("token:blocklist:%x", tokenHash[:])

	err := redisClient.Set(ctx, blocklistKey, "revoked", ttl).Err()
	if err != nil {
		log.Error().Err(err).Str("tokenHash", fmt.Sprintf("%x", tokenHash[:4])).Msg("Failed to blocklist token")
	}
}

func BlocklistUserRefreshTokens(ctx context.Context, redisClient *redis.Client, userID string) {
	refreshKey := fmt.Sprintf("refresh:blocklist:%s", userID)
	err := redisClient.Set(ctx, refreshKey, "revoked", 7*24*time.Hour).Err()
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to blocklist refresh tokens")
	}
}

func ExtractTokenExpiry(token string) (time.Time, error) {
	parsed, _, err := new(jwt.Parser).ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing token: %w", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return time.Time{}, fmt.Errorf("invalid claims")
	}
	expFloat, ok := claims["exp"].(float64)
	if !ok {
		return time.Time{}, fmt.Errorf("missing exp claim")
	}
	return time.Unix(int64(expFloat), 0), nil
}
