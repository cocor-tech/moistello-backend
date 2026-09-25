package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/response"
)

// PasskeyHandler handles passkey-based authentication: nonce generation and
// signature verification against a user's wallet, plus linking a passkey
// credential to an existing account.
type PasskeyHandler struct {
	authService auth.Service
	userService user.Service
	userRepo    user.Repository
}

// NewPasskeyHandler builds the passkey authentication handler.
func NewPasskeyHandler(authSvc auth.Service, userSvc user.Service, userRepo user.Repository) *PasskeyHandler {
	return &PasskeyHandler{authService: authSvc, userService: userSvc, userRepo: userRepo}
}

// PasskeyNonce generates a nonce for the wallet backing a passkey credential,
// starting the passkey authentication flow.
//
// @Summary Get passkey authentication nonce
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Passkey credential" { "credentialId": "string" }
// @Success 200 {object} response.Envelope{data=object{nonce=object,walletAddress=string}}
// @Failure 400 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /auth/passkey/nonce [post]
func (h *PasskeyHandler) PasskeyNonce(c *gin.Context) {
	var req struct {
		CredentialID string `json:"credentialId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "credentialId is required")
		return
	}

	u, err := h.userRepo.FindByPasskeyCredentialID(c.Request.Context(), req.CredentialID)
	if err != nil {
		response.NotFound(c, "passkey not linked to any account")
		return
	}

	nonce, err := h.authService.GenerateNonce(c.Request.Context(), u.WalletAddress)
	if err != nil {
		response.InternalError(c, "failed to generate nonce")
		return
	}

	response.OK(c, gin.H{"nonce": nonce, "walletAddress": u.WalletAddress})
}

// PasskeyVerify verifies a passkey-signed nonce and creates a session for the
// linked account.
//
// @Summary Verify passkey authentication
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Passkey signature" { "credentialId": "string", "signature": "string" }
// @Success 200 {object} response.Envelope{data=object{token=string,refreshToken=string}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Router /auth/passkey/verify [post]
func (h *PasskeyHandler) PasskeyVerify(c *gin.Context) {
	var req struct {
		CredentialID string `json:"credentialId" binding:"required"`
		Signature    string `json:"signature" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "credentialId and signature are required")
		return
	}

	u, err := h.userRepo.FindByPasskeyCredentialID(c.Request.Context(), req.CredentialID)
	if err != nil {
		response.NotFound(c, "passkey not linked to any account")
		return
	}

	valid, err := h.authService.VerifySignature(c.Request.Context(), u.WalletAddress, req.Signature)
	if err != nil || !valid {
		response.Unauthorized(c, "signature verification failed")
		return
	}

	pair, err := h.authService.CreateSession(c.Request.Context(), u.ID, string(u.Role), sessionTTLFromUser(u), deviceInfoFromContext(c))
	if err != nil {
		response.InternalError(c, "failed to create session")
		return
	}

	response.OK(c, gin.H{
		"token": pair.AccessToken, "refreshToken": pair.RefreshToken, "csrfToken": pair.CSRFToken, "user": u,
	})
}

// PasskeyLink links a passkey credential to the authenticated user's account.
//
// @Summary Link passkey credential
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Passkey credential" { "credentialId": "string" }
// @Success 200 {object} response.Envelope{data=object{success=bool}}
// @Failure 400 {object} response.Envelope
// @Router /auth/passkey/link [post]
func (h *PasskeyHandler) PasskeyLink(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var req struct {
		CredentialID string `json:"credentialId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "credentialId is required")
		return
	}

	u, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil {
		response.NotFound(c, "user not found")
		return
	}

	u.PasskeyCredentialID = &req.CredentialID
	if err := h.userRepo.Update(c.Request.Context(), u); err != nil {
		response.InternalError(c, "failed to link passkey")
		return
	}

	response.OK(c, gin.H{"success": true})
}
