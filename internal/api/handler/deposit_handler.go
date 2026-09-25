package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api/middleware"
	"github.com/moistello/backend/internal/domain/deposit"
	"github.com/moistello/backend/internal/domain/featureflag"
	"github.com/moistello/backend/internal/domain/fx"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/internal/domain/withdrawal"
	"github.com/moistello/backend/internal/domain/yellowcard"
	"github.com/moistello/backend/pkg/money"
	"github.com/moistello/backend/pkg/response"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// rateQuoteTTL bounds how long a fetched FX rate is reused before the
// pricing path re-queries the provider. See fx.CachingRateProvider.
const rateQuoteTTL = 30 * time.Second

// defaultCurrency is used when a request omits an explicit currency, so
// existing NGN-only callers keep working unchanged.
const defaultCurrency = "NGN"

type DepositHandler struct {
	yc           *yellowcard.Client
	wallet       wallet.Service
	rdb          *redis.Client
	cfg          config.YellowCardConfig
	deposits     deposit.Repository
	withdrawals  withdrawal.Repository
	flags        *featureflag.Cache
	rateProvider fx.RateProvider
}

func NewDepositHandler(yc *yellowcard.Client, walletSvc wallet.Service) *DepositHandler {
	return &DepositHandler{
		yc:     yc,
		wallet: walletSvc,
		// Wrapped in a cache+fallback decorator (issue #189) so a
		// transient Yellow Card outage doesn't hard-fail every quote —
		// see fx.CachingRateProvider's doc comment for the fallback
		// semantics.
		rateProvider: fx.NewCachingRateProvider(fx.NewYellowCardRateProvider(yc), rateQuoteTTL),
	}
}

func (h *DepositHandler) WithRedis(rdb *redis.Client) *DepositHandler {
	h.rdb = rdb
	return h
}

func (h *DepositHandler) WithConfig(cfg config.YellowCardConfig) *DepositHandler {
	h.cfg = cfg
	return h
}

// WithRepositories wires persistence for deposits and withdrawals so their
// state survives process restarts and can be reconciled against Yellow Card
// webhook notifications instead of only living in the initial API response.
func (h *DepositHandler) WithRepositories(deposits deposit.Repository, withdrawals withdrawal.Repository) *DepositHandler {
	h.deposits = deposits
	h.withdrawals = withdrawals
	return h
}

// WithFeatureFlags wires the runtime-reloadable feature flag cache (#187)
// so this handler can gate behavior (e.g. kyc_required) without a redeploy.
func (h *DepositHandler) WithFeatureFlags(flags *featureflag.Cache) *DepositHandler {
	h.flags = flags
	return h
}

// WithRateProvider overrides the default cached Yellow Card provider —
// primarily for tests, but also lets a future multi-source aggregator be
// swapped in without touching the handler's request-handling logic.
func (h *DepositHandler) WithRateProvider(rp fx.RateProvider) *DepositHandler {
	h.rateProvider = rp
	return h
}

// resolveCurrency normalizes and validates a request-supplied currency
// code, defaulting to NGN when empty. Returns an error message suitable
// for direct display to the caller when the currency isn't supported.
func resolveCurrency(raw string) (string, string) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if code == "" {
		code = defaultCurrency
	}
	if !fx.IsSupportedFiatCurrency(code) {
		return "", fmt.Sprintf("unsupported currency %q", code)
	}
	return code, ""
}

// currencyCaps resolves the per-transaction/daily caps for a currency.
// NGN falls back to the legacy top-level config fields for backward
// compatibility with existing deployments; any other currency must have an
// explicit entry in cfg.CurrencyCaps, since moving real money in a new
// corridor without a deliberately configured cap is a business risk this
// handler won't take on by default. ok is false when the currency has no
// caps configured, meaning deposits/withdrawals aren't enabled for it yet
// even though it may still be quotable via GetDepositQuote.
func (h *DepositHandler) currencyCaps(currency string) (caps config.CurrencyCaps, ok bool) {
	if c, found := h.cfg.CurrencyCaps[currency]; found {
		return c, true
	}
	if currency == defaultCurrency {
		return config.CurrencyCaps{
			MaxDeposit:       h.maxDepositNGN(),
			MaxWithdraw:      h.maxWithdrawUSDC(),
			DailyDepositCap:  h.dailyDepositCapNGN(),
			DailyWithdrawCap: h.dailyWithdrawCapUSDC(),
		}, true
	}
	return config.CurrencyCaps{}, false
}

func (h *DepositHandler) maxDepositNGN() float64 {
	if h.cfg.MaxDepositNGN > 0 {
		return h.cfg.MaxDepositNGN
	}
	return 5_000_000 // 5M NGN default per-transaction cap
}

func (h *DepositHandler) maxWithdrawUSDC() float64 {
	if h.cfg.MaxWithdrawUSDC > 0 {
		return h.cfg.MaxWithdrawUSDC
	}
	return 10_000 // 10k USDC default per-transaction cap
}

func (h *DepositHandler) dailyDepositCapNGN() float64 {
	if h.cfg.DailyDepositCapNGN > 0 {
		return h.cfg.DailyDepositCapNGN
	}
	return 10_000_000 // 10M NGN default daily cap
}

func (h *DepositHandler) dailyWithdrawCapUSDC() float64 {
	if h.cfg.DailyWithdrawCapUSDC > 0 {
		return h.cfg.DailyWithdrawCapUSDC
	}
	return 20_000 // 20k USDC default daily cap
}

func getIdempotencyKey(c *gin.Context, bodyKey string) string {
	if bodyKey != "" {
		return strings.TrimSpace(bodyKey)
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		key = strings.TrimSpace(c.GetHeader("X-Idempotency-Key"))
	}
	return key
}

// GetDepositQuote returns a <currency>→USDC quote. currency defaults to NGN
// and must be one of fx.SupportedFiatCurrencies.
// GET /v1/wallet/deposit/quote?amount=50000&currency=NGN
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

	currency, errMsg := resolveCurrency(c.Query("currency"))
	if errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}

	quote, err := h.rateProvider.GetQuote(c.Request.Context(), currency, "USDC", amount)
	if err != nil {
		response.InternalError(c, "failed to get quote: "+err.Error())
		return
	}

	response.OK(c, gin.H{"quote": quote})
}

// InitiateDeposit creates a deposit request (<currency> → USDC). currency
// defaults to NGN. A currency needs an entry in
// config.YellowCardConfig.CurrencyCaps (or to be NGN, which falls back to
// the legacy top-level cap fields) to be accepted here — see
// (*DepositHandler).currencyCaps.
// POST /v1/wallet/deposit
func (h *DepositHandler) InitiateDeposit(c *gin.Context) {
	userID := middleware.GetUserID(c)

	var req struct {
		// AmountNGN is the deposit amount in the request currency. The
		// field name predates multi-currency support (issue #189) and is
		// kept for backward compatibility with existing NGN-only callers;
		// it is not restricted to NGN when Currency is set to something
		// else.
		AmountNGN      float64 `json:"amountNgn" binding:"required,gt=0"`
		Currency       string  `json:"currency"`
		IdempotencyKey string  `json:"idempotencyKey"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "amountNgn is required")
		return
	}

	currency, errMsg := resolveCurrency(req.Currency)
	if errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}
	caps, capsOK := h.currencyCaps(currency)
	if !capsOK {
		response.BadRequest(c, fmt.Sprintf("deposits are not yet enabled for currency %q", currency))
		return
	}

	idempotencyKey := getIdempotencyKey(c, req.IdempotencyKey)
	ctx := c.Request.Context()

	// Check idempotency cache in Redis
	if idempotencyKey != "" && h.rdb != nil {
		cachedKey := fmt.Sprintf("yc:idempotency:deposit:%s:%s:%s", userID, currency, idempotencyKey)
		cachedData, err := h.rdb.Get(ctx, cachedKey).Bytes()
		if err == nil && len(cachedData) > 0 {
			var cachedResp gin.H
			if err := json.Unmarshal(cachedData, &cachedResp); err == nil {
				response.OK(c, cachedResp)
				return
			}
		}
	}

	// Validate per-transaction amount cap
	if req.AmountNGN > caps.MaxDeposit {
		response.BadRequest(c, fmt.Sprintf("deposit amount exceeds maximum allowed limit of %.2f %s", caps.MaxDeposit, currency))
		return
	}

	// Validate daily amount cap
	if h.rdb != nil {
		today := time.Now().UTC().Format("2006-01-02")
		dailyKey := fmt.Sprintf("yc:daily:deposit:%s:%s:%s", userID, currency, today)
		currentDaily, _ := h.rdb.Get(ctx, dailyKey).Float64()
		if currentDaily+req.AmountNGN > caps.DailyDepositCap {
			response.BadRequest(c, fmt.Sprintf("deposit amount exceeds daily limit of %.2f %s (current total: %.2f %s)", caps.DailyDepositCap, currency, currentDaily, currency))
			return
		}
	}

	// Get user's primary wallet
	wallets, err := h.wallet.GetWallets(ctx, userID)
	if err != nil || len(wallets) == 0 {
		response.BadRequest(c, "no wallet found. Create a wallet first.")
		return
	}
	userWallet := wallets[0]

	// Get quote
	quote, err := h.rateProvider.GetQuote(ctx, currency, "USDC", req.AmountNGN)
	if err != nil {
		response.InternalError(c, "failed to get quote: "+err.Error())
		return
	}

	// Create receive request
	paymentRef := fmt.Sprintf("MOIST-%d", time.Now().UnixMilli())
	receive, err := h.yc.CreateReceive(yellowcard.ReceiveRequest{
		Amount:              req.AmountNGN,
		Currency:            currency,
		DestinationCurrency: "USDC",
		DestinationAddress:  userWallet.PublicKey,
		PaymentReference:    paymentRef,
	})
	if err != nil {
		response.InternalError(c, "failed to create deposit: "+err.Error())
		return
	}

	// Persist the deposit so its state survives process restarts and can be
	// reconciled against Yellow Card webhook notifications. Yellow Card has
	// already accepted the receive request at this point, so a persistence
	// failure here is logged loudly for manual reconciliation rather than
	// silently discarded — the deposit still exists on Yellow Card's side.
	if h.deposits != nil {
		var expiresAt *time.Time
		if receive.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, receive.ExpiresAt); err == nil {
				expiresAt = &t
			}
		}
		d := &deposit.Deposit{
			ID:                 uuid.New().String(),
			UserID:             userID,
			AmountNGN:          req.AmountNGN,
			EstimatedUSDC:      quote.ToAmount,
			DestinationAddress: userWallet.PublicKey,
			Status:             deposit.DepositStatusPending,
			ReceiveID:          receive.ReceiveID,
			PaymentRef:         paymentRef,
			CreatedAt:          time.Now(),
			ExpiresAt:          expiresAt,
		}
		if err := h.deposits.Create(ctx, d); err != nil {
			log.Error().Err(err).
				Str("receiveId", receive.ReceiveID).
				Str("paymentRef", paymentRef).
				Str("userId", userID).
				Msg("failed to persist deposit after Yellow Card accepted it — requires manual reconciliation")
			response.InternalError(c, "failed to record deposit")
			return
		}
	}

	respData := gin.H{
		"deposit": gin.H{
			"receiveId":     receive.ReceiveID,
			"paymentRef":    paymentRef,
			"currency":      currency,
			"bankDetails":   receive.BankDetails,
			"estimatedUsdc": quote.ToAmount,
			"spread":        quote.FeePercentage,
			"expiresAt":     receive.ExpiresAt,
		},
	}

	// Update daily total and cache idempotency
	if h.rdb != nil {
		today := time.Now().UTC().Format("2006-01-02")
		dailyKey := fmt.Sprintf("yc:daily:deposit:%s:%s:%s", userID, currency, today)
		h.rdb.IncrByFloat(ctx, dailyKey, req.AmountNGN)
		h.rdb.Expire(ctx, dailyKey, 48*time.Hour)

		if idempotencyKey != "" {
			cachedKey := fmt.Sprintf("yc:idempotency:deposit:%s:%s:%s", userID, currency, idempotencyKey)
			if payload, err := json.Marshal(respData); err == nil {
				h.rdb.Set(ctx, cachedKey, payload, 24*time.Hour)
			}
		}
	}

	response.Created(c, respData)
}

// InitiateWithdraw creates a withdrawal request (USDC → <currency>).
// currency defaults to NGN and, like InitiateDeposit, needs an entry in
// config.YellowCardConfig.CurrencyCaps (or to be NGN) to be accepted.
// POST /v1/wallet/withdraw
func (h *DepositHandler) InitiateWithdraw(c *gin.Context) {
	userID := middleware.GetUserID(c)

	if h.flags != nil && h.flags.IsEnabled("kyc_required") {
		response.ErrorWithCode(c, http.StatusPreconditionRequired, "kyc_required",
			"KYC verification is required before withdrawals, but KYC submission is not yet available")
		return
	}

	var req struct {
		AmountUSDC     float64 `json:"amountUsdc" binding:"required,gt=0"`
		Currency       string  `json:"currency"`
		BankCode       string  `json:"bankCode" binding:"required"`
		AccountNumber  string  `json:"accountNumber" binding:"required"`
		AccountName    string  `json:"accountName" binding:"required"`
		IdempotencyKey string  `json:"idempotencyKey"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "amountUsdc, bankCode, accountNumber, and accountName are required")
		return
	}

	currency, errMsg := resolveCurrency(req.Currency)
	if errMsg != "" {
		response.BadRequest(c, errMsg)
		return
	}
	caps, capsOK := h.currencyCaps(currency)
	if !capsOK {
		response.BadRequest(c, fmt.Sprintf("withdrawals are not yet enabled for currency %q", currency))
		return
	}

	idempotencyKey := getIdempotencyKey(c, req.IdempotencyKey)
	ctx := c.Request.Context()

	// Check idempotency cache in Redis
	if idempotencyKey != "" && h.rdb != nil {
		cachedKey := fmt.Sprintf("yc:idempotency:withdraw:%s:%s:%s", userID, currency, idempotencyKey)
		cachedData, err := h.rdb.Get(ctx, cachedKey).Bytes()
		if err == nil && len(cachedData) > 0 {
			var cachedResp gin.H
			if err := json.Unmarshal(cachedData, &cachedResp); err == nil {
				response.OK(c, cachedResp)
				return
			}
		}
	}

	// Validate per-transaction amount cap
	if req.AmountUSDC > caps.MaxWithdraw {
		response.BadRequest(c, fmt.Sprintf("withdrawal amount exceeds maximum allowed limit of %.2f USDC", caps.MaxWithdraw))
		return
	}

	// Validate daily amount cap
	if h.rdb != nil {
		today := time.Now().UTC().Format("2006-01-02")
		dailyKey := fmt.Sprintf("yc:daily:withdraw:%s:%s:%s", userID, currency, today)
		currentDaily, _ := h.rdb.Get(ctx, dailyKey).Float64()
		if currentDaily+req.AmountUSDC > caps.DailyWithdrawCap {
			response.BadRequest(c, fmt.Sprintf("withdrawal amount exceeds daily limit of %.2f USDC (current total: %.2f USDC)", caps.DailyWithdrawCap, currentDaily))
			return
		}
	}

	// Get user's primary wallet
	wallets, err := h.wallet.GetWallets(ctx, userID)
	if err != nil || len(wallets) == 0 {
		response.BadRequest(c, "no wallet found. Create a wallet first.")
		return
	}
	userWallet := wallets[0]

	// Get quote
	quote, err := h.rateProvider.GetQuote(ctx, "USDC", currency, req.AmountUSDC)
	if err != nil {
		response.InternalError(c, "failed to get quote: "+err.Error())
		return
	}

	// Create send request
	paymentRef := fmt.Sprintf("MOIST-%d", time.Now().UnixMilli())
	sendResp, err := h.yc.CreateSend(yellowcard.SendRequest{
		Amount:         req.AmountUSDC,
		Currency:       "USDC",
		TargetCurrency: currency,
		BankCode:       req.BankCode,
		AccountNumber:  req.AccountNumber,
		AccountName:    req.AccountName,
		PaymentRef:     paymentRef,
	})
	if err != nil {
		response.InternalError(c, "failed to create withdrawal: "+err.Error())
		return
	}

	// Return Yellow Card's configured Stellar address for the user to send USDC
	// to. The address is provided at startup from config rather than hard-coded.
	ycAddress := h.yc.StellarAddress()

	// Persist the withdrawal so its state survives process restarts and can
	// be reconciled against Yellow Card webhook notifications. Yellow Card
	// has already accepted the send request at this point, so a persistence
	// failure here is logged loudly for manual reconciliation rather than
	// silently discarded.
	if h.withdrawals != nil {
		amountUSDC, err := money.FromFloat64(req.AmountUSDC)
		if err != nil {
			response.BadRequest(c, "invalid amountUsdc")
			return
		}
		estimatedNGN, err := money.FromFloat64(quote.ToAmount)
		if err != nil {
			response.InternalError(c, "invalid quote amount")
			return
		}
		wd := &withdrawal.Withdrawal{
			ID:              uuid.New().String(),
			UserID:          userID,
			AmountUSDC:      amountUSDC,
			EstimatedNGN:    estimatedNGN,
			BankCode:        req.BankCode,
			AccountNumber:   req.AccountNumber,
			AccountName:     req.AccountName,
			Status:          withdrawal.WithdrawalStatusPending,
			PlatformAddress: ycAddress,
			PaymentRef:      paymentRef,
			CreatedAt:       time.Now(),
		}
		if err := h.withdrawals.Create(ctx, wd); err != nil {
			log.Error().Err(err).
				Str("sendId", sendResp.SendID).
				Str("paymentRef", paymentRef).
				Str("userId", userID).
				Msg("failed to persist withdrawal after Yellow Card accepted it — requires manual reconciliation")
			response.InternalError(c, "failed to record withdrawal")
			return
		}
		if err := h.withdrawals.UpdateYellowCardTxID(ctx, wd.ID, sendResp.SendID); err != nil {
			log.Error().Err(err).Str("withdrawalId", wd.ID).Msg("failed to record yellow card send id")
		}
	}

	respData := gin.H{
		"withdraw": gin.H{
			"sendId":            sendResp.SendID,
			"status":            sendResp.Status,
			"paymentRef":        paymentRef,
			"currency":          currency,
			"estimatedNgn":      quote.ToAmount,
			"spread":            quote.FeePercentage,
			"yellowCardAddress": ycAddress,
			"usdcAmount":        req.AmountUSDC,
			"userWallet":        userWallet.PublicKey,
		},
	}

	// Update daily total and cache idempotency
	if h.rdb != nil {
		today := time.Now().UTC().Format("2006-01-02")
		dailyKey := fmt.Sprintf("yc:daily:withdraw:%s:%s:%s", userID, currency, today)
		h.rdb.IncrByFloat(ctx, dailyKey, req.AmountUSDC)
		h.rdb.Expire(ctx, dailyKey, 48*time.Hour)

		if idempotencyKey != "" {
			cachedKey := fmt.Sprintf("yc:idempotency:withdraw:%s:%s:%s", userID, currency, idempotencyKey)
			if payload, err := json.Marshal(respData); err == nil {
				h.rdb.Set(ctx, cachedKey, payload, 24*time.Hour)
			}
		}
	}

	response.OK(c, respData)
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
