package handler

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

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
				pipe.Exec(ctx)
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

func sha256HashForLogout(s string) string {
	hash := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", hash)
}
