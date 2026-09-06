package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/response"
	"github.com/moistello/backend/pkg/stellar"
)

// WalletAuthHandler handles wallet-based authentication: obtaining a signed
// nonce and verifying a wallet signature to establish a session.
type WalletAuthHandler struct {
	authService auth.Service
	userService user.Service
}

// NewWalletAuthHandler builds a wallet authentication handler.
func NewWalletAuthHandler(authSvc auth.Service, userSvc user.Service) *WalletAuthHandler {
	return &WalletAuthHandler{authService: authSvc, userService: userSvc}
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
func (h *WalletAuthHandler) Nonce(c *gin.Context) {
	var req struct {
		WalletAddress string `json:"walletAddress" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := stellar.ValidateAddress(req.WalletAddress); err != nil {
		response.BadRequest(c, "invalid wallet address: "+err.Error())
		return
	}

	nonce, err := h.authService.GenerateNonce(c.Request.Context(), req.WalletAddress)
	if err != nil {
		response.InternalError(c, "failed to generate nonce")
		return
	}
	response.OK(c, gin.H{"nonce": nonce})
}

// @Summary Verify wallet authentication
// @Description Verifies a signed nonce and creates a session.
// @Tags Authentication
// @Accept json
// @Produce json
// @Param body body object true "Signature payload" { "walletAddress": "G...", "signature": "..." }
// @Success 200 {object} response.Envelope{data=object{token=string,refreshToken=string}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Router /auth/verify [post]
func (h *WalletAuthHandler) Verify(c *gin.Context) {
	var req struct {
		WalletAddress string `json:"walletAddress" binding:"required"`
		Signature     string `json:"signature" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	u, err := h.userService.GetByWallet(c.Request.Context(), req.WalletAddress)
	if err != nil {
		response.NotFound(c, "account not found")
		return
	}

	valid, err := h.authService.VerifySignature(c.Request.Context(), req.WalletAddress, req.Signature)
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
