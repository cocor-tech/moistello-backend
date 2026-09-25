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
// public method surface used by the router and existing tests (#155). The
// wallet authentication flows (nonce/verify) live in WalletAuthHandler,
// session management (refresh/me/logout/list/revoke) in SessionHandler, the
// email registration flow in RegistrationHandler, email/password login in
// LoginHandler, passkey auth in PasskeyHandler, TOTP 2FA in TOTPHandler,
// backup-code recovery in RecoveryHandler, and wallet initialization in
// WalletInitHandler.
type AuthHandler struct {
	*WalletAuthHandler
	*SessionHandler
	*RegistrationHandler
	*LoginHandler
	*PasskeyHandler
	*TOTPHandler
	*RecoveryHandler
	*WalletInitHandler
}

// NewAuthHandler builds the auth handler aggregate. The signature is kept for
// backward compatibility; each focused sub-handler consumes the dependencies
// it actually needs.
func NewAuthHandler(authSvc auth.Service, userSvc user.Service, walletSvc wallet.Service,
	totpSvc *totp.Service, verificationSvc *verification.Service, _ *email.Service,
	redisClient *redis.Client, userRepo user.Repository) *AuthHandler {
	if totpSvc == nil {
		totpSvc = totp.NewService()
	}
	return &AuthHandler{
		WalletAuthHandler:   NewWalletAuthHandler(authSvc, userSvc),
		SessionHandler:      NewSessionHandler(authSvc, userSvc, redisClient),
		RegistrationHandler: NewRegistrationHandler(authSvc, userRepo, verificationSvc, walletSvc),
		LoginHandler:        NewLoginHandler(authSvc, userSvc, verificationSvc),
		PasskeyHandler:      NewPasskeyHandler(authSvc, userSvc, userRepo),
		TOTPHandler:         NewTOTPHandler(userSvc, userRepo, totpSvc),
		RecoveryHandler:     NewRecoveryHandler(authSvc, userSvc, userRepo, totpSvc),
		WalletInitHandler:   NewWalletInitHandler(userSvc, walletSvc),
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
