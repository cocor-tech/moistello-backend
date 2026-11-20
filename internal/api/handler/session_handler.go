package handler

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/response"
)

// SessionHandler handles session lifecycle: token refresh, current-profile
// lookup, and logout / session revocation.
type SessionHandler struct {
	authService auth.Service
	userService user.Service
	redisClient *redis.Client
}

// NewSessionHandler builds a session management handler.
func NewSessionHandler(authSvc auth.Service, userSvc user.Service, redisClient *redis.Client) *SessionHandler {
	return &SessionHandler{authService: authSvc, userService: userSvc, redisClient: redisClient}
}

// @Summary Refresh JWT tokens
// @Description Exchanges a valid refresh token for a new access token and refresh token pair.
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Refresh token" { "refreshToken": "string" }
// @Success 200 {object} response.Envelope{data=object{token=string,refreshToken=string}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Router /auth/refresh [post]
func (h *SessionHandler) Refresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refreshToken" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	tokenPair, err := h.authService.RefreshToken(c.Request.Context(), req.RefreshToken)
	if err != nil {
		response.Unauthorized(c, "invalid refresh token")
		return
	}
	if !h.sessionMatchesCurrentRole(c, tokenPair) {
		response.Unauthorized(c, "session was invalidated by an account change; sign in again")
		return
	}
	response.OK(c, gin.H{"token": tokenPair.AccessToken, "refreshToken": tokenPair.RefreshToken, "csrfToken": tokenPair.CSRFToken})
}

// @Summary Get current user profile
// @Description Returns the authenticated user's profile. Requires Bearer token. Replaces the old POST /auth/me endpoint.
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{user=object}}
// @Failure 401 {object} response.Envelope
// @Router /me [get]
func (h *SessionHandler) Me(c *gin.Context) {
	userID := middleware.GetUserID(c)
	u, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil {
		response.Unauthorized(c, "user not found")
		return
	}
	response.OK(c, gin.H{"user": u})
}

// @Summary Logout / Terminate Session
// @Description Invalidates the current session and all refresh tokens. REST standard: DELETE /v1/auth/sessions
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Router /auth/sessions [delete]
func (h *SessionHandler) Logout(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		response.Unauthorized(c, "missing or invalid token")
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")

	userID := middleware.GetUserID(c)
	ctx := c.Request.Context()

	if h.redisClient != nil {
		// 1. Blocklist the access token
		if expiry, err := middleware.ExtractTokenExpiry(token); err == nil {
			middleware.BlocklistToken(ctx, h.redisClient, token, expiry)
		}

		// 2. Delete all user sessions from Redis
		if userID != "" {
			userSessionsKey := fmt.Sprintf("user:sessions:%s", userID)
			sessionHashes, err := h.redisClient.SMembers(ctx, userSessionsKey).Result()
			if err == nil {
				pipe := h.redisClient.Pipeline()
				for _, hash := range sessionHashes {
					pipe.Del(ctx, fmt.Sprintf("session:%s", hash))
				}
				pipe.Del(ctx, userSessionsKey)
				if _, err := pipe.Exec(ctx); err != nil {
					log.Warn().Err(err).Str("userId", userID).Msg("failed to delete user sessions from redis")
				}
			}

			// 3. Set blocklist key for any missed sessions
			middleware.BlocklistUserRefreshTokens(ctx, h.redisClient, userID)
		}

		// 4. If refresh token was provided in body, also delete that specific session
		var req struct {
			RefreshToken string `json:"refreshToken"`
		}
		if err := c.ShouldBindJSON(&req); err == nil && req.RefreshToken != "" {
			tokenHash := sha256HashForLogout(req.RefreshToken)
			sessionKey := fmt.Sprintf("session:%s", tokenHash)
			h.redisClient.Del(ctx, sessionKey)
		}
	}

	response.OK(c, gin.H{"success": true})
}

// @Summary Revoke specific session by ID/hash
// @Description Revokes a specific session.
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Param id path string true "Session Hash / ID"
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Router /auth/sessions/{id} [delete]
func (h *SessionHandler) RevokeSessionByID(c *gin.Context) {
	sessionID := c.Param("id")
	userID := middleware.GetUserID(c)
	ctx := c.Request.Context()

	if h.redisClient != nil && sessionID != "" {
		// If the id is a session hash, delete it directly, or remove from user sessions
		sessionKey := fmt.Sprintf("session:%s", sessionID)
		h.redisClient.Del(ctx, sessionKey)
		if userID != "" {
			userSessionsKey := fmt.Sprintf("user:sessions:%s", userID)
			h.redisClient.SRem(ctx, userSessionsKey, sessionID)
		}
	}

	response.OK(c, gin.H{"success": true})
}

// @Summary Change password and rotate the session
// @Description Updates the password, revokes every session (including the calling one) and returns a replacement token pair, so no session from before the change survives it.
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Change password payload"
// @Success 200 {object} response.Envelope{data=object{success=bool,token=string,refreshToken=string,csrfToken=string}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Router /auth/password/change [post]
func (h *SessionHandler) ChangePassword(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "authentication required")
		return
	}

	var req struct {
		OldPassword string `json:"oldPassword" binding:"required"`
		NewPassword string `json:"newPassword" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	u, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil || u == nil {
		response.Unauthorized(c, "user not found")
		return
	}

	if u.PasswordHash.Valid && u.PasswordHash.String != "" {
		if !auth.VerifyPassword(req.OldPassword, u.PasswordHash.String) {
			response.BadRequest(c, "incorrect existing password")
			return
		}
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		response.InternalError(c, "failed to hash new password")
		return
	}

	u.PasswordHash = sql.NullString{String: newHash, Valid: true}
	if err := h.userService.Update(c.Request.Context(), u); err != nil {
		response.InternalError(c, "failed to update password")
		return
	}

	resp := gin.H{"success": true, "message": "password changed; every session was replaced"}
	newSessionRotator(h.authService, h.redisClient).rotate(c, u, resp)

	log.Info().Str("userID", userID).Str("event", "auth.password_changed.sessions_revoked").Msg("password changed, all sessions invalidated")

	response.OK(c, resp)
}

// sessionMatchesCurrentRole reports whether a freshly refreshed session still
// carries the user's current role. Sessions copy the role they were created
// with, so a promotion or demotion would otherwise never reach a session
// minted before it (#459). On a mismatch every session is revoked so the user
// signs in again at the new privilege level.
func (h *SessionHandler) sessionMatchesCurrentRole(c *gin.Context, pair *auth.TokenPair) bool {
	claims, err := h.authService.ValidateJWT(pair.AccessToken)
	if err != nil {
		return false
	}
	u, err := h.userService.GetByID(c.Request.Context(), claims.UserID)
	if err != nil {
		return false
	}
	if claims.Role == currentRole(u) {
		return true
	}

	ctx := c.Request.Context()
	if err := h.authService.RevokeAllSessions(ctx, claims.UserID, ""); err != nil {
		log.Error().Err(err).Str("userID", claims.UserID).Msg("role change: failed to revoke existing sessions")
	}
	if h.redisClient != nil {
		if expiry, err := middleware.ExtractTokenExpiry(pair.AccessToken); err == nil {
			middleware.BlocklistToken(ctx, h.redisClient, pair.AccessToken, expiry)
		}
	}
	log.Warn().Str("userID", claims.UserID).Str("sessionRole", claims.Role).Str("currentRole", currentRole(u)).
		Str("security_event", "session.role_mismatch").Msg("refresh refused: session predates a role change")
	return false
}

func sha256HashForLogout(s string) string {
	hash := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", hash)
}

// @Summary List all active sessions for the current user
// @Description Returns a list of all active sessions with device information (#472).
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{sessions=array}}
// @Failure 401 {object} response.Envelope
// @Router /auth/sessions [get]
func (h *SessionHandler) ListSessions(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "authentication required")
		return
	}

	ctx := c.Request.Context()
	sessions := []gin.H{}

	if h.redisClient != nil {
		userSessionsKey := fmt.Sprintf("user:sessions:%s", userID)
		sessionHashes, err := h.redisClient.SMembers(ctx, userSessionsKey).Result()
		if err == nil {
			for _, hash := range sessionHashes {
				sessionKey := fmt.Sprintf("session:%s", hash)
				sessionData, err := h.redisClient.HGetAll(ctx, sessionKey).Result()
				if err != nil || len(sessionData) == 0 {
					continue
				}

				// Determine if this is the current session
				currentHash := ""
				if authHeader := c.GetHeader("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
					token := strings.TrimPrefix(authHeader, "Bearer ")
					currentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
				}

				sessions = append(sessions, gin.H{
					"id":          hash,
					"device":      sessionData["device"],
					"user_agent":  sessionData["user_agent"],
					"ip_address":  sessionData["ip_address"],
					"created_at":  sessionData["created_at"],
					"last_active": sessionData["last_active"],
					"is_current":  hash == currentHash,
				})
			}
		}
	}

	response.OK(c, gin.H{"sessions": sessions})
}

// @Summary Revoke a specific device session
// @Description Revokes a specific device session by its ID (#472).
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Param id path string true "Session ID"
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Failure 401 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /auth/sessions/{id} [delete]
func (h *SessionHandler) RevokeDeviceSession(c *gin.Context) {
	sessionID := c.Param("id")
	userID := middleware.GetUserID(c)
	ctx := c.Request.Context()

	if sessionID == "" || userID == "" {
		response.BadRequest(c, "session ID and authentication required")
		return
	}

	if h.redisClient != nil {
		// Verify the session belongs to this user
		userSessionsKey := fmt.Sprintf("user:sessions:%s", userID)
		isMember, err := h.redisClient.SIsMember(ctx, userSessionsKey, sessionID).Result()
		if err != nil || !isMember {
			response.NotFound(c, "session not found or not owned by user")
			return
		}

		// Delete the session
		sessionKey := fmt.Sprintf("session:%s", sessionID)
		pipe := h.redisClient.Pipeline()
		pipe.Del(ctx, sessionKey)
		pipe.SRem(ctx, userSessionsKey, sessionID)
		if _, err := pipe.Exec(ctx); err != nil {
			log.Warn().Err(err).Str("sessionId", sessionID).Msg("failed to delete session from redis")
		}

		response.OK(c, gin.H{"success": true})
	} else {
		response.OK(c, gin.H{"success": true})
	}
}
