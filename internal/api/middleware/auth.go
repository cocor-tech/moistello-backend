package middleware

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
)

type Claims struct {
	jwt.RegisteredClaims
	UserID string `json:"sub"`
	Wallet string `json:"wallet"`
	Role   string `json:"role"`
}

func AuthMiddleware(publicKeyPEM []byte) gin.HandlerFunc {
	publicKey, err := parseRSAPublicKey(publicKeyPEM)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to parse JWT public key")
	}

	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "error": "missing authorization header"})
			return
		}
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "error": "invalid authorization format"})
			return
		}
		token, err := jwt.ParseWithClaims(parts[1], &Claims{}, func(t *jwt.Token) (any, error) {
			return publicKey, nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "error": "invalid or expired token"})
			return
		}
		claims, ok := token.Claims.(*Claims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "error": "invalid token claims"})
			return
		}
		c.Set("userID", claims.UserID)
		c.Set("wallet", claims.Wallet)
		c.Set("role", claims.Role)
		log.Debug().Str("userID", claims.UserID).Str("path", c.Request.URL.Path).Msg("authenticated request")
		c.Next()
	}
}

func OptionalAuthMiddleware(publicKeyPEM []byte) gin.HandlerFunc {
	publicKey, err := parseRSAPublicKey(publicKeyPEM)
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
		token, err := jwt.ParseWithClaims(parts[1], &Claims{}, func(t *jwt.Token) (any, error) { return publicKey, nil })
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

func GetUserID(c *gin.Context) string { id, _ := c.Get("userID"); return id.(string) }
func GetWallet(c *gin.Context) string  { w, _ := c.Get("wallet"); return w.(string) }
func GetRole(c *gin.Context) string    { r, _ := c.Get("role"); return r.(string) }

func parseRSAPublicKey(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block for public key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is not RSA public key")
	}
	return rsaKey, nil
}
