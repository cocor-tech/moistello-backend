package handler

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/internal/domain/yellowcard"
	"github.com/moistello/backend/pkg/response"
)

type DepositHandler struct {
	yc     *yellowcard.Client
	wallet wallet.Service
}

func NewDepositHandler(yc *yellowcard.Client, walletSvc wallet.Service) *DepositHandler {
	return &DepositHandler{yc: yc, wallet: walletSvc}
}

// GetDepositQuote returns a NGN→USDC quote
// GET /v1/wallet/deposit/quote?amount=50000
func (h *DepositHandler) GetDepositQuote(c *gin.Context) {
	amountStr := c.Query("amount")
	if amountStr == "" {
		response.BadRequest(c, "amount is required")
		return
	}

	var amount float64
	if _, err := fmt.Sscanf(amountStr, "%f", &amount); err != nil || amount <= 0 {
		response.BadRequest(c, "invalid amount")
		return
	}

	quote, err := h.yc.GetQuote("NGN", "USDC", amount)
	if err != nil {
		response.InternalError(c, "failed to get quote: "+err.Error())
		return
	}

	response.OK(c, gin.H{"quote": quote})
}

// InitiateDeposit creates a deposit request (NGN → USDC)
// POST /v1/wallet/deposit
func (h *DepositHandler) InitiateDeposit(c *gin.Context) {
	userID := middleware.GetUserID(c)

	var req struct {
		AmountNGN float64 `json:"amountNgn" binding:"required,gt=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "amountNgn is required")
		return
	}

	// Get user's primary wallet
	wallets, err := h.wallet.GetWallets(c.Request.Context(), userID)
	if err != nil || len(wallets) == 0 {
		response.BadRequest(c, "no wallet found. Create a wallet first.")
		return
	}
	userWallet := wallets[0]

	// Get quote
	quote, err := h.yc.GetQuote("NGN", "USDC", req.AmountNGN)
	if err != nil {
		response.InternalError(c, "failed to get quote: "+err.Error())
		return
	}

	// Create receive request
	paymentRef := fmt.Sprintf("MOIST-%d", time.Now().UnixMilli())
	receive, err := h.yc.CreateReceive(yellowcard.ReceiveRequest{
		Amount:              req.AmountNGN,
		Currency:            "NGN",
		DestinationCurrency: "USDC",
		DestinationAddress:  userWallet.PublicKey,
		PaymentReference:    paymentRef,
	})
	if err != nil {
		response.InternalError(c, "failed to create deposit: "+err.Error())
		return
	}

	response.Created(c, gin.H{
		"deposit": gin.H{
			"receiveId":     receive.ReceiveID,
			"paymentRef":    paymentRef,
			"bankDetails":   receive.BankDetails,
			"estimatedUsdc": quote.ToAmount,
			"spread":        quote.FeePercentage,
			"expiresAt":     receive.ExpiresAt,
		},
	})
}

// POST /v1/wallet/withdraw
func (h *DepositHandler) InitiateWithdraw(c *gin.Context) {
	userID := middleware.GetUserID(c)

	var req struct {
		AmountUSDC    float64 `json:"amountUsdc" binding:"required,gt=0"`
		BankCode      string  `json:"bankCode" binding:"required"`
		AccountNumber string  `json:"accountNumber" binding:"required"`
		AccountName   string  `json:"accountName" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "amountUsdc, bankCode, accountNumber, and accountName are required")
		return
	}

	// Get user's primary wallet
	wallets, err := h.wallet.GetWallets(c.Request.Context(), userID)
	if err != nil || len(wallets) == 0 {
		response.BadRequest(c, "no wallet found. Create a wallet first.")
		return
	}
	userWallet := wallets[0]

	// Get quote
	quote, err := h.yc.GetQuote("USDC", "NGN", req.AmountUSDC)
	if err != nil {
		response.InternalError(c, "failed to get quote: "+err.Error())
		return
	}

	// Create send request
	paymentRef := fmt.Sprintf("MOIST-%d", time.Now().UnixMilli())
	sendResp, err := h.yc.CreateSend(yellowcard.SendRequest{
		Amount:        req.AmountUSDC,
		Currency:      "USDC",
		TargetCurrency: "NGN",
		BankCode:      req.BankCode,
		AccountNumber: req.AccountNumber,
		AccountName:   req.AccountName,
		PaymentRef:    paymentRef,
	})
	if err != nil {
		response.InternalError(c, "failed to create withdrawal: "+err.Error())
		return
	}

	// Return Yellow Card's Stellar address for the user to send USDC to
	ycAddress := "GABCDEF123..." // This comes from Yellow Card's API config

	response.OK(c, gin.H{
		"withdraw": gin.H{
			"sendId":             sendResp.SendID,
			"status":             sendResp.Status,
			"paymentRef":         paymentRef,
			"estimatedNgn":       quote.ToAmount,
			"spread":             quote.FeePercentage,
			"yellowCardAddress":  ycAddress,
			"usdcAmount":         req.AmountUSDC,
			"userWallet":         userWallet.PublicKey,
		},
	})
}

// GET /v1/wallet/transactions/:yellowCardId
func (h *DepositHandler) GetTransactionStatus(c *gin.Context) {
	txnID := c.Param("yellowCardId")
	status, err := h.yc.GetTransactionStatus(txnID)
	if err != nil {
		response.InternalError(c, "failed to get status: "+err.Error())
		return
	}
	response.OK(c, gin.H{"transaction": status})
}
