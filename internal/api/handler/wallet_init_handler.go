package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/pkg/response"
)

// WalletInitHandler creates the on-chain Stellar wallet for an email-based
// account from its deterministic, email-derived seed. Seed derivation lives
// in wallet.Service.DeriveWalletSeed (consolidated in #163/#166), so handlers
// no longer touch crypto/argon2 or the pepper config directly.
type WalletInitHandler struct {
	userService user.Service
	walletSvc   wallet.Service
}

// NewWalletInitHandler builds the wallet initialization handler.
func NewWalletInitHandler(userSvc user.Service, walletSvc wallet.Service) *WalletInitHandler {
	return &WalletInitHandler{userService: userSvc, walletSvc: walletSvc}
}

// InitWallet creates the user's Stellar wallet from their email-derived seed,
// recovering from a failed auto-create during registration (#115).
//
// @Summary Initialize on-chain wallet
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 201 {object} response.Envelope{data=object{wallet=object}}
// @Failure 400 {object} response.Envelope
// @Failure 401 {object} response.Envelope
// @Router /auth/wallet/init [post]
func (h *WalletInitHandler) InitWallet(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "not authenticated")
		return
	}

	u, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil {
		response.NotFound(c, "user not found")
		return
	}

	email := ""
	if u.Email != nil {
		email = *u.Email
	}
	if email == "" {
		response.BadRequest(c, "no email on account")
		return
	}

	seed, err := h.walletSvc.DeriveWalletSeed(c.Request.Context(), email)
	if err != nil {
		response.InternalError(c, "wallet seed generation failed: "+err.Error())
		return
	}
	w, err := h.walletSvc.CreateWallet(c.Request.Context(), userID, []byte(seed))
	if err != nil {
		response.InternalError(c, "wallet creation failed: "+err.Error())
		return
	}

	response.Created(c, gin.H{"wallet": w})
}
