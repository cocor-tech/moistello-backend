package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/token"
	"github.com/moistello/backend/pkg/response"
)

type TokenHandler struct {
	tokenSvc token.Service
}

func NewTokenHandler(svc token.Service) *TokenHandler {
	return &TokenHandler{tokenSvc: svc}
}

// GetBalance returns the token balance for a given address
// GET /v1/token/balance/:address
func (h *TokenHandler) GetBalance(c *gin.Context) {
	address := c.Param("address")
	if address == "" {
		response.BadRequest(c, "address is required")
		return
	}

	balance, err := h.tokenSvc.GetBalance(c.Request.Context(), address)
	if err != nil {
		response.InternalError(c, "failed to get balance: "+err.Error())
		return
	}

	response.OK(c, gin.H{"address": address, "balance": balance})
}

// Stake stakes tokens
// POST /v1/token/stake
func (h *TokenHandler) Stake(c *gin.Context) {
	userID := middleware.GetUserID(c)

	var req struct {
		Amount uint64 `json:"amount" binding:"required,gt=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	txHash, err := h.tokenSvc.Stake(c.Request.Context(), userID, req.Amount)
	if err != nil {
		response.InternalError(c, "stake failed: "+err.Error())
		return
	}

	response.OK(c, gin.H{"txHash": txHash, "amount": req.Amount})
}

// Unstake unstakes tokens
// POST /v1/token/unstake
func (h *TokenHandler) Unstake(c *gin.Context) {
	userID := middleware.GetUserID(c)

	var req struct {
		Amount uint64 `json:"amount" binding:"required,gt=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	txHash, err := h.tokenSvc.Unstake(c.Request.Context(), userID, req.Amount)
	if err != nil {
		response.InternalError(c, "unstake failed: "+err.Error())
		return
	}

	response.OK(c, gin.H{"txHash": txHash, "amount": req.Amount})
}

// GetStakes returns the staked amount for a given address
// GET /v1/token/stakes/:address
func (h *TokenHandler) GetStakes(c *gin.Context) {
	address := c.Param("address")
	if address == "" {
		response.BadRequest(c, "address is required")
		return
	}

	stakedAmount, err := h.tokenSvc.GetStakedAmount(c.Request.Context(), address)
	if err != nil {
		response.InternalError(c, "failed to get staked amount: "+err.Error())
		return
	}

	response.OK(c, gin.H{"address": address, "staked_amount": stakedAmount})
}
