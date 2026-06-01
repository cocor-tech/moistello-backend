package handler

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/response"
)

type AuthHandler struct {
	authService      auth.Service
	userService      user.Service
	redisClient      *redis.Client
	verificationSvc  *auth.VerificationService
}

func NewAuthHandler(authSvc auth.Service, userSvc user.Service, redisClient *redis.Client, verificationSvc *auth.VerificationService) *AuthHandler {
	return &AuthHandler{
		authService:     authSvc,
		userService:     userSvc,
		redisClient:     redisClient,
		verificationSvc: verificationSvc,
	}
}
// @Summary Get authentication nonce
// @Description Returns a signed nonce for wallet authentication. The nonce must be signed with the wallet's private key and sent to /auth/verify.
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Wallet address" { "walletAddress": "G..." }
// @Success 200 {object} response.Envelope{data=object{nonce=string}}
// @Failure 400 {object} response.Envelope
// @Router /auth/nonce [post]
func (h *AuthHandler) Nonce(c *gin.Context) {
	var req struct {
		WalletAddress string `json:"walletAddress" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	nonce, err := h.authService.GenerateNonce(c.Request.Context(), req.WalletAddress)
	if err != nil {
		response.InternalError(c, "failed to generate nonce")
		return
	}
	response.OK(c, gin.H{"nonce": nonce})
}

// @Summary Verify wallet signature and login
// @Description Verifies a signed nonce to prove wallet ownership. Creates a user account if one doesn't exist. Returns JWT tokens.
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Signature payload" { "walletAddress": "G...", "signature": "base64_signed_nonce" }
// @Success 200 {object} response.Envelope{data=object{token=string,refreshToken=string,user=object}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Router /auth/verify [post]
func (h *AuthHandler) Verify(c *gin.Context) {
	var req struct {
		WalletAddress string `json:"walletAddress" binding:"required"`
		Signature     string `json:"signature" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	valid, err := h.authService.VerifySignature(c.Request.Context(), req.WalletAddress, req.Signature)
	if err != nil || !valid {
		response.Unauthorized(c, "signature verification failed")
		return
	}
	u, err := h.userService.Create(c.Request.Context(), req.WalletAddress)
	if err != nil {
		response.InternalError(c, "failed to create user")
		return
	}
	tokenPair, err := h.authService.CreateSession(c.Request.Context(), u.ID)
	if err != nil {
		response.InternalError(c, "failed to create session")
		return
	}
	response.OK(c, gin.H{"token": tokenPair.AccessToken, "refreshToken": tokenPair.RefreshToken, "user": u})
}

// @Summary Register new user with profile
// @Description Verifies wallet signature and creates a user account with optional profile fields (displayName, email, countryCode, language). Returns JWT tokens.
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Registration payload"
// @Success 200 {object} response.Envelope{data=object{token=string,refreshToken=string,user=object}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Router /auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req struct {
		WalletAddress  string  `json:"walletAddress" binding:"required"`
		Signature      string  `json:"signature" binding:"required"`
		DisplayName    *string `json:"displayName"`
		Email          *string `json:"email"`
		CountryCode    *string `json:"countryCode"`
		Language       *string `json:"language"`
		VerificationID string  `json:"verificationId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if h.verificationSvc != nil && req.Email != nil && *req.Email != "" {
		if req.VerificationID == "" {
			response.Forbidden(c, "Email verification is required. Provide verificationId.")
			return
		}
		if err := h.verificationSvc.CheckEmailVerified(c.Request.Context(), *req.Email, req.VerificationID); err != nil {
			var verifyErr *auth.VerifyError
			if errors.As(err, &verifyErr) {
				status := verifyErr.StatusCode
				if status == 0 {
					status = 403
				}
				c.JSON(status, gin.H{"success": false, "error": verifyErr.Message})
				return
			}
			response.Forbidden(c, "Email not verified. Please complete email verification first.")
			return
		}
	}

	valid, err := h.authService.VerifySignature(c.Request.Context(), req.WalletAddress, req.Signature)
	if err != nil || !valid {
		response.Unauthorized(c, "signature verification failed")
		return
	}
	u, err := h.userService.Create(c.Request.Context(), req.WalletAddress)
	if err != nil {
		response.InternalError(c, "failed to create user")
		return
	}
	updates := user.UpdateProfileInput{
		DisplayName:       req.DisplayName,
		Email:             req.Email,
		CountryCode:       req.CountryCode,
		PreferredLanguage: req.Language,
	}
	u, err = h.userService.UpdateProfile(c.Request.Context(), u.ID.String(), updates)
	if err != nil {
		response.InternalError(c, "failed to update profile")
		return
	}
	tokenPair, err := h.authService.CreateSession(c.Request.Context(), u.ID)
	if err != nil {
		response.InternalError(c, "failed to create session")
		return
	}
	response.OK(c, gin.H{"token": tokenPair.AccessToken, "refreshToken": tokenPair.RefreshToken, "user": u})
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
func (h *AuthHandler) Refresh(c *gin.Context) {
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
	response.OK(c, gin.H{"token": tokenPair.AccessToken, "refreshToken": tokenPair.RefreshToken})
}

// @Summary Get current user
// @Description Returns the authenticated user's profile. Requires Bearer token.
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{user=object}}
// @Failure 401 {object} response.Envelope
// @Router /auth/me [post]
func (h *AuthHandler) Me(c *gin.Context) {
	userID := middleware.GetUserID(c)
	u, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil {
		response.Unauthorized(c, "user not found")
		return
	}
	response.OK(c, gin.H{"user": u})
}

// @Summary Logout
// @Description Invalidates the current session.
// @Tags Authentication
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Router /auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		response.Unauthorized(c, "missing or invalid token")
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")

	expiresAt, err := middleware.ExtractTokenExpiry(token)
	if err != nil {
		response.BadRequest(c, "invalid token")
		return
	}

	if h.redisClient != nil {
		middleware.BlocklistToken(c.Request.Context(), h.redisClient, token, expiresAt)

		userID := middleware.GetUserID(c)
		if userID != "" {
			middleware.BlocklistUserRefreshTokens(c.Request.Context(), h.redisClient, userID)
		}
	}

	response.OK(c, gin.H{"success": true})
}
