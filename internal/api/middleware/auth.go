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

func AuthMiddleware(publicKeyPEM []byte, previousPublicKeyPEM ...[]byte) gin.HandlerFunc {
	keys, err := parseVerifyingKeys(publicKeyPEM, previousPublicKeyPEM...)
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
		claims, valid := validateTokenWithKeys(parts[1], keys)
		if !valid || claims == nil {
			c.Abort()
			response.Unauthorized(c, "invalid or expired token")
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

func OptionalAuthMiddleware(publicKeyPEM []byte, previousPublicKeyPEM ...[]byte) gin.HandlerFunc {
	keys, err := parseVerifyingKeys(publicKeyPEM, previousPublicKeyPEM...)
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
		claims, valid := validateTokenWithKeys(parts[1], keys)
		if !valid || claims == nil {
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

type verifyingKeyInfo struct {
	key          any
	verifyingAlg string
	kid          string
}

func parseVerifyingKeys(publicKeyPEM []byte, previousPublicKeyPEM ...[]byte) ([]verifyingKeyInfo, error) {
	allPEMs := append([][]byte{publicKeyPEM}, previousPublicKeyPEM...)
	var keys []verifyingKeyInfo

	for _, pemBytes := range allPEMs {
		if len(pemBytes) == 0 {
			continue
		}
		parsedKeys, err := auth.ParsePublicVerifyingKeys(pemBytes)
		if err != nil {
			vk, method, parseErr := auth.ParsePublicVerifyingKey(pemBytes)
			if parseErr != nil {
				return nil, err
			}
			keys = append(keys, verifyingKeyInfo{
				key:          vk,
				verifyingAlg: method.Alg(),
				kid:          "",
			})
			continue
		}
		for _, k := range parsedKeys {
			keys = append(keys, verifyingKeyInfo{
				key:          k.VerifyingKey,
				verifyingAlg: k.VerifyingAlg,
				kid:          k.ID,
			})
		}
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid public keys provided")
	}
	return keys, nil
}

func validateTokenWithKeys(tokenStr string, keys []verifyingKeyInfo) (*Claims, bool) {
	parser := jwt.NewParser()
	var tokenKID string
	if unverifiedToken, _, err := parser.ParseUnverified(tokenStr, &Claims{}); err == nil && unverifiedToken != nil {
		if kid, ok := unverifiedToken.Header["kid"].(string); ok {
			tokenKID = kid
		}
	}

	orderedKeys := make([]verifyingKeyInfo, 0, len(keys))
	if tokenKID != "" {
		for _, k := range keys {
			if k.kid != "" && k.kid == tokenKID {
				orderedKeys = append(orderedKeys, k)
			}
		}
	}
	for _, k := range keys {
		alreadyAdded := false
		for _, ok := range orderedKeys {
			if ok.key == k.key {
				alreadyAdded = true
				break
			}
		}
		if !alreadyAdded {
			orderedKeys = append(orderedKeys, k)
		}
	}

	for _, kInfo := range orderedKeys {
		algName := kInfo.verifyingAlg
		if algName == "" {
			algName = "RS256"
		}
		token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != algName {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return kInfo.key, nil
		}, jwt.WithValidMethods([]string{algName}))

		if err == nil && token != nil && token.Valid {
			if claims, ok := token.Claims.(*Claims); ok {
				return claims, true
			}
		}
	}

	return nil, false
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
// configured primary and secondary admin API keys for zero-downtime key rotation.
// Used to protect internal endpoints like /metrics.
func AdminAPIKeyMiddleware(keys ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var primary, secondary string
		if len(keys) > 0 {
			primary = keys[0]
		}
		if len(keys) > 1 {
			secondary = keys[1]
		}

		if primary == "" && secondary == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"error":   "admin API key not configured",
			})
			return
		}

		headerKey := c.GetHeader("X-Admin-API-Key")
		if headerKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "invalid admin API key",
			})
			return
		}

		if primary != "" && headerKey == primary {
			c.Set("adminKeyIdentity", "primary")
			metrics.AdminKeyRequestsTotal.WithLabelValues("primary").Inc()
			log.Debug().Str("identity", "primary").Str("path", c.Request.URL.Path).Msg("authenticated with primary admin API key")
			c.Next()
			return
		}

		if secondary != "" && headerKey == secondary {
			c.Set("adminKeyIdentity", "secondary")
			metrics.AdminKeyRequestsTotal.WithLabelValues("secondary").Inc()
			log.Debug().Str("identity", "secondary").Str("path", c.Request.URL.Path).Msg("authenticated with secondary admin API key")
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "invalid admin API key",
		})
	}
}
