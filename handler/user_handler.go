package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"src/services"
)

type UserHandler struct {
	userService  services.UserService
	tokenService services.TokenService
}

func NewUserHandler(userService services.UserService, tokenService services.TokenService) *UserHandler {
	return &UserHandler{
		userService:  userService,
		tokenService: tokenService,
	}
}

// DeleteAccount handles permanent account removal and immediate JWT revocation.
func (h *UserHandler) DeleteAccount(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized context"})
		return
	}

	// 1. Extract Bearer token from authorization header to invalidate current token
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenStr != authHeader {
			// Blocklist token until max possible natural expiration window (4 hours)
			if err := h.tokenService.BlocklistToken(c.Request.Context(), tokenStr, 4*time.Hour); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to revoke active token"})
				return
			}
		}
	}

	// 2. Blocklist all outstanding refresh/access tokens registered for user
	if err := h.tokenService.BlocklistAllUserTokens(c.Request.Context(), userID, 4*time.Hour); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to revoke active user sessions"})
		return
	}

	// 3. Delete user record
	if err := h.userService.DeleteUser(c.Request.Context(), userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user account"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Account deleted successfully and all tokens revoked"})
}