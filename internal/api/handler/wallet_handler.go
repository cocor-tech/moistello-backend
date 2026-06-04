package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/pkg/response"
)

type WalletHandler struct {
	walletSvc wallet.Service
}

func NewWalletHandler(svc wallet.Service) *WalletHandler {
	return &WalletHandler{walletSvc: svc}
}

// CreateWallet creates a new Stellar wallet for the authenticated user
// POST /v1/wallets
func (h *WalletHandler) CreateWallet(c *gin.Context) {
	userID := middleware.GetUserID(c)

	var req struct {
		PasskeySeed string `json:"passkeySeed" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "passkeySeed is required")
		return
	}

	w, err := h.walletSvc.CreateWallet(c.Request.Context(), userID, []byte(req.PasskeySeed))
	if err != nil {
		response.InternalError(c, "failed to create wallet: "+err.Error())
		return
	}

	response.Created(c, gin.H{"wallet": w})
}

// ListWallets returns all wallets for the authenticated user
// GET /v1/wallets
func (h *WalletHandler) ListWallets(c *gin.Context) {
	userID := middleware.GetUserID(c)
	wallets, err := h.walletSvc.GetWallets(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "failed to list wallets")
		return
	}
	response.OK(c, gin.H{"wallets": wallets})
}

// DeleteWallet deletes a wallet by ID
// DELETE /v1/wallets/:id
func (h *WalletHandler) DeleteWallet(c *gin.Context) {
	userID := middleware.GetUserID(c)
	walletID := c.Param("id")
	if err := h.walletSvc.DeleteWallet(c.Request.Context(), userID, walletID); err != nil {
		response.InternalError(c, "failed to delete wallet")
		return
	}
	response.OK(c, gin.H{"success": true})
}
