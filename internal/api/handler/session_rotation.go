package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/user"
)

// sessionRotator regenerates a user's session after a privilege change (#459):
// every existing session is revoked, the access token that made the request is
// blocklisted, and a fresh token pair is issued at the user's current role. A
// session established before the change — including one an attacker planted
// or captured — therefore cannot carry over to the new privilege level.
type sessionRotator struct {
	authService auth.Service
	redisClient *redis.Client
}

// newSessionRotator returns nil (a no-op rotator) when no auth service is
// wired, as in handler tests that exercise only domain behaviour.
func newSessionRotator(authSvc auth.Service, redisClient *redis.Client) *sessionRotator {
	if authSvc == nil {
		return nil
	}
	return &sessionRotator{authService: authSvc, redisClient: redisClient}
}

// rotate replaces the caller's session and adds the new token, refreshToken and
// csrfToken to resp. If no new session can be issued, resp gets
// reauthRequired=true instead; the old sessions are revoked either way.
func (r *sessionRotator) rotate(c *gin.Context, u *user.User, resp gin.H) {
	if r == nil {
		return
	}
	ctx := c.Request.Context()
	userID := u.ID.String()

	if err := r.authService.RevokeAllSessions(ctx, userID, ""); err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("privilege change: failed to revoke existing sessions")
	}
	if token := bearerToken(c); token != "" && r.redisClient != nil {
		if expiry, err := middleware.ExtractTokenExpiry(token); err == nil {
			middleware.BlocklistToken(ctx, r.redisClient, token, expiry)
		}
	}

	pair, err := r.authService.CreateSession(ctx, u.ID, currentRole(u), sessionTTLFromUser(u), deviceInfoFromContext(c))
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("privilege change: failed to issue a replacement session")
		resp["reauthRequired"] = true
		return
	}
	log.Info().Str("userID", userID).Str("security_event", "session.rotated").Msg("session regenerated after privilege change")
	resp["token"] = pair.AccessToken
	resp["refreshToken"] = pair.RefreshToken
	resp["csrfToken"] = pair.CSRFToken
}

// currentRole is the role a session for u must carry; session storage treats
// an unset role as "user".
func currentRole(u *user.User) string {
	if u.Role == "" {
		return string(user.RoleUser)
	}
	return string(u.Role)
}

func bearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(header, "Bearer ")
}
