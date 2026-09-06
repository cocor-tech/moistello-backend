package handler

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/email"
	"github.com/moistello/backend/internal/domain/totp"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/domain/verification"
	"github.com/moistello/backend/internal/domain/wallet"
)

// AuthHandler aggregates the focused auth sub-handlers while preserving the
// public method surface used by the router and existing tests. The wallet
// authentication flows (nonce/verify) live in WalletAuthHandler, session
// management (refresh/me/logout/revoke) in SessionHandler, and the email
// registration flow in RegistrationHandler.
type AuthHandler struct {
	*WalletAuthHandler
	*SessionHandler
	*RegistrationHandler
}

// NewAuthHandler builds the auth handler aggregate. The signature is kept for
// backward compatibility; each focused sub-handler consumes the dependencies
// it actually needs.
func NewAuthHandler(authSvc auth.Service, userSvc user.Service, walletSvc wallet.Service,
	_ *totp.Service, verificationSvc *verification.Service, _ *email.Service,
	redisClient *redis.Client, userRepo user.Repository) *AuthHandler {
	return &AuthHandler{
		WalletAuthHandler:   NewWalletAuthHandler(authSvc, userSvc),
		SessionHandler:      NewSessionHandler(authSvc, userSvc, redisClient),
		RegistrationHandler: NewRegistrationHandler(authSvc, userRepo, verificationSvc, walletSvc),
	}
}

// sessionTTLFromUser returns the configured session TTL, falling back to the
// default 240 minutes when the user has not set one.
func sessionTTLFromUser(u *user.User) time.Duration {
	ttl := u.SessionTTLMinutes
	if ttl < 60 {
		ttl = 240
	}
	return time.Duration(ttl) * time.Minute
}

// deviceInfoFromContext builds a stable device fingerprint from the request.
func deviceInfoFromContext(c *gin.Context) string {
	ua := c.GetHeader("User-Agent")
	if ua == "" {
		ua = "unknown"
	}
	ip := c.ClientIP()
	if ip == "" {
		ip = "unknown"
	}
	return fmt.Sprintf("%s|%s", ua, ip)
}
