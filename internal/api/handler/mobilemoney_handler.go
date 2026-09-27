package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/mobilemoney"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/pkg/apperrors"
	"github.com/moistello/backend/pkg/response"
)

// MobileMoneyHandler exposes the mobile-money bridge (#190) — on-ramp
// (mobile money -> USDC) and off-ramp (USDC -> mobile money) for non-NGN
// markets — following the same request shape as DepositHandler's NGN
// bridge via Yellow Card.
type MobileMoneyHandler struct {
	svc    mobilemoney.Service
	wallet wallet.Service
}

func NewMobileMoneyHandler(svc mobilemoney.Service, walletSvc wallet.Service) *MobileMoneyHandler {
	return &MobileMoneyHandler{svc: svc, wallet: walletSvc}
}

// @Summary Initiate mobile-money on-ramp
// @Description Collects funds from the caller's mobile money wallet and credits USDC to their Stellar wallet once settled. Requires an idempotency key.
// @Tags Wallet
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object{amount=number,currency=string,phoneNumber=string,idempotencyKey=string} true "On-ramp request"
// @Success 201 {object} response.Envelope{data=object{transaction=object}}
// @Failure 400 {object} response.Envelope
// @Router /wallet/mobile-money/onramp [post]
func (h *MobileMoneyHandler) InitiateOnramp(c *gin.Context) {
	userID := middleware.GetUserID(c)

	var req struct {
		Amount         float64 `json:"amount" binding:"required,gt=0"`
		Currency       string  `json:"currency" binding:"required"`
		PhoneNumber    string  `json:"phoneNumber" binding:"required"`
		IdempotencyKey string  `json:"idempotencyKey"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "amount, currency, and phoneNumber are required")
		return
	}

	idempotencyKey := getIdempotencyKey(c, req.IdempotencyKey)
	if idempotencyKey == "" {
		response.BadRequest(c, "an idempotency key is required (Idempotency-Key header or idempotencyKey field)")
		return
	}

	ctx := c.Request.Context()
	wallets, err := h.wallet.GetWallets(ctx, userID)
	if err != nil || len(wallets) == 0 {
		response.BadRequest(c, "no wallet found. Create a wallet first.")
		return
	}

	txn, err := h.svc.InitiateOnramp(ctx, userID, mobilemoney.OnrampRequest{
		Amount:             req.Amount,
		Currency:           req.Currency,
		PhoneNumber:        req.PhoneNumber,
		DestinationAddress: wallets[0].PublicKey,
		Reference:          idempotencyKey,
	}, idempotencyKey)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.Created(c, gin.H{"transaction": txn})
}

// @Summary Initiate mobile-money off-ramp
// @Description Disburses USDC from the caller's account to their mobile money wallet. Requires an idempotency key.
// @Tags Wallet
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object{amount=number,currency=string,phoneNumber=string,accountName=string,idempotencyKey=string} true "Off-ramp request"
// @Success 201 {object} response.Envelope{data=object{transaction=object}}
// @Failure 400 {object} response.Envelope
// @Router /wallet/mobile-money/offramp [post]
func (h *MobileMoneyHandler) InitiateOfframp(c *gin.Context) {
	userID := middleware.GetUserID(c)

	var req struct {
		Amount         float64 `json:"amount" binding:"required,gt=0"`
		Currency       string  `json:"currency" binding:"required"`
		PhoneNumber    string  `json:"phoneNumber" binding:"required"`
		AccountName    string  `json:"accountName" binding:"required"`
		IdempotencyKey string  `json:"idempotencyKey"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "amount, currency, phoneNumber, and accountName are required")
		return
	}

	idempotencyKey := getIdempotencyKey(c, req.IdempotencyKey)
	if idempotencyKey == "" {
		response.BadRequest(c, "an idempotency key is required (Idempotency-Key header or idempotencyKey field)")
		return
	}

	txn, err := h.svc.InitiateOfframp(c.Request.Context(), userID, mobilemoney.OfframpRequest{
		Amount:      req.Amount,
		Currency:    req.Currency,
		PhoneNumber: req.PhoneNumber,
		AccountName: req.AccountName,
		Reference:   idempotencyKey,
	}, idempotencyKey)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.Created(c, gin.H{"transaction": txn})
}

// @Summary Get mobile-money transaction
// @Description Returns a single mobile-money on-ramp/off-ramp transaction by ID. Only the owning user may view it.
// @Tags Wallet
// @Produce json
// @Security BearerAuth
// @Param id path string true "Transaction ID"
// @Success 200 {object} response.Envelope{data=object{transaction=object}}
// @Failure 404 {object} response.Envelope
// @Router /wallet/mobile-money/{id} [get]
func (h *MobileMoneyHandler) GetTransaction(c *gin.Context) {
	txn, err := h.svc.GetTransaction(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.NotFound(c, "transaction not found")
			return
		}
		response.InternalError(c, "failed to get transaction")
		return
	}

	if txn.UserID != middleware.GetUserID(c) {
		response.NotFound(c, "transaction not found")
		return
	}

	response.OK(c, gin.H{"transaction": txn})
}

// @Summary List supported mobile-money providers and currencies
// @Description Returns active mobile-money providers and their supported fiat currencies for on-ramp/off-ramp options.
// @Tags Wallet
// @Produce json
// @Success 200 {object} response.Envelope{data=object{providers=array,supportedCurrencies=array}}
// @Router /wallet/mobile-money/providers [get]
func (h *MobileMoneyHandler) ListProviders(c *gin.Context) {
	providers := h.svc.ListProviders(c.Request.Context())
	currencies := h.svc.GetSupportedCurrencies(c.Request.Context())
	response.OK(c, gin.H{
		"providers":           providers,
		"supportedCurrencies": currencies,
	})
}

