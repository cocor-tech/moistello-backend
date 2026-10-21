package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/featureflag"
	ffmocks "github.com/moistello/backend/internal/domain/featureflag/mocks"
	"github.com/moistello/backend/internal/domain/fx"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/internal/domain/yellowcard"
)

type mockDepositWalletService struct {
	wallets []wallet.Wallet
}

func (m *mockDepositWalletService) CreateWallet(ctx context.Context, userID string, passkeySeed []byte) (*wallet.Wallet, error) {
	return nil, nil
}
func (m *mockDepositWalletService) SignTransaction(ctx context.Context, walletID string, passkeySeed []byte, txnXDR string) (string, error) {
	return "", nil
}
func (m *mockDepositWalletService) GetWallets(ctx context.Context, userID string) ([]wallet.Wallet, error) {
	return m.wallets, nil
}
func (m *mockDepositWalletService) GetBalance(ctx context.Context, userID string) (*wallet.Balance, error) {
	return nil, nil
}
func (m *mockDepositWalletService) SendPayment(ctx context.Context, userID string, passkeySeed []byte, destination, asset string, amount float64, memo, ipAddress, userAgent string) (string, error) {
	return "", nil
}
func (m *mockDepositWalletService) DeleteWallet(ctx context.Context, userID, walletID string) error {
	return nil
}
func (m *mockDepositWalletService) DeriveWalletSeed(ctx context.Context, email string) (string, error) {
	return "mock-seed", nil
}

func setupTestDepositRouter(h *handler.DepositHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "test-user-123")
		c.Next()
	})
	r.GET("/v1/wallet/deposit/quote", h.GetDepositQuote)
	r.POST("/v1/wallet/deposit", h.InitiateDeposit)
	r.POST("/v1/wallet/withdraw", h.InitiateWithdraw)
	return r
}

// stubRateProvider lets tests exercise the multi-currency pricing path
// (issue #189) without a real Yellow Card client/network call.
type stubRateProvider struct {
	rate float64
	err  error
}

func (s *stubRateProvider) GetQuote(_ context.Context, from, to string, amount float64) (fx.Quote, error) {
	if s.err != nil {
		return fx.Quote{}, s.err
	}
	return fx.Quote{FromCurrency: from, ToCurrency: to, FromAmount: amount, ToAmount: amount * s.rate, Rate: s.rate}, nil
}

func TestDepositHandler_MultiCurrencyQuote(t *testing.T) {
	ycClient := yellowcard.NewClient("", "", "")
	mockWallet := &mockDepositWalletService{}

	h := handler.NewDepositHandler(ycClient, mockWallet).
		WithRateProvider(&stubRateProvider{rate: 0.0075})
	r := setupTestDepositRouter(h)

	t.Run("defaults to NGN when currency is omitted", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/wallet/deposit/quote?amount=1000", nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"fromCurrency":"NGN"`)
	})

	t.Run("accepts a supported non-NGN currency", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/wallet/deposit/quote?amount=1000&currency=KES", nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"fromCurrency":"KES"`)
	})

	t.Run("is case-insensitive on the currency code", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/wallet/deposit/quote?amount=1000&currency=kes", nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("rejects an unsupported currency", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/wallet/deposit/quote?amount=1000&currency=USD", nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "unsupported currency")
	})

	t.Run("falls through to the caching provider's error when the provider fails", func(t *testing.T) {
		hFailing := handler.NewDepositHandler(ycClient, mockWallet).
			WithRateProvider(&stubRateProvider{err: assert.AnError})
		rFailing := setupTestDepositRouter(hFailing)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/wallet/deposit/quote?amount=1000", nil)
		rFailing.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestDepositHandler_CurrencyWithoutConfiguredCapsIsRejected(t *testing.T) {
	ycClient := yellowcard.NewClient("", "", "")
	mockWallet := &mockDepositWalletService{
		wallets: []wallet.Wallet{{PublicKey: "GABC12345"}},
	}

	// No CurrencyCaps entry for KES, and it isn't NGN, so deposits/withdrawals
	// in KES must be rejected even though KES is a supported quote currency.
	h := handler.NewDepositHandler(ycClient, mockWallet).
		WithRateProvider(&stubRateProvider{rate: 0.0075}).
		WithConfig(config.YellowCardConfig{MaxDepositNGN: 500_000})
	r := setupTestDepositRouter(h)

	t.Run("deposit in an un-capped currency is rejected", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]any{
			"amountNgn": 1000,
			"currency":  "KES",
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/wallet/deposit", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "not yet enabled")
	})

	t.Run("deposit in KES succeeds validation once caps are configured for it", func(t *testing.T) {
		hWithCaps := handler.NewDepositHandler(ycClient, mockWallet).
			WithRateProvider(&stubRateProvider{rate: 0.0075}).
			WithConfig(config.YellowCardConfig{
				CurrencyCaps: map[string]config.CurrencyCaps{
					"KES": {MaxDeposit: 100, DailyDepositCap: 1000},
				},
			})
		rWithCaps := setupTestDepositRouter(hWithCaps)

		reqBody, _ := json.Marshal(map[string]any{
			"amountNgn": 1000, // exceeds the configured 100 KES cap
			"currency":  "KES",
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/wallet/deposit", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		rWithCaps.ServeHTTP(w, req)

		// Now that KES has caps configured, the rejection reason changes
		// from "not enabled" to the ordinary per-transaction cap check —
		// proving the currency was accepted and routed through the same
		// cap-enforcement logic as NGN, just with KES's own limits.
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "exceeds maximum allowed limit")
		assert.NotContains(t, w.Body.String(), "not yet enabled")
	})
}

func TestDepositHandler_AmountCaps(t *testing.T) {
	ycClient := yellowcard.NewClient("", "", "")
	mockWallet := &mockDepositWalletService{
		wallets: []wallet.Wallet{{PublicKey: "GABC12345"}},
	}

	h := handler.NewDepositHandler(ycClient, mockWallet).WithConfig(config.YellowCardConfig{
		MaxDepositNGN:   100_000,
		MaxWithdrawUSDC: 500,
	})

	r := setupTestDepositRouter(h)

	t.Run("deposit exceeding max cap is rejected", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]any{
			"amountNgn": 150_000,
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/wallet/deposit", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "exceeds maximum allowed limit")
	})

	t.Run("withdraw exceeding max cap is rejected", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]any{
			"amountUsdc":    600,
			"bankCode":      "044",
			"accountNumber": "0123456789",
			"accountName":   "John Doe",
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/wallet/withdraw", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "exceeds maximum allowed limit")
	})
}

func TestDepositHandler_DailyCapsAndIdempotency(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer func() { _ = rdb.Close() }()

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skip("Redis is not available, skipping live daily caps & idempotency test")
	}

	ctx := context.Background()
	ycClient := yellowcard.NewClient("", "", "")
	mockWallet := &mockDepositWalletService{
		wallets: []wallet.Wallet{{PublicKey: "GABC12345"}},
	}

	h := handler.NewDepositHandler(ycClient, mockWallet).
		WithRedis(rdb).
		WithConfig(config.YellowCardConfig{
			MaxDepositNGN:      500_000,
			DailyDepositCapNGN: 100_000,
		})

	r := setupTestDepositRouter(h)

	// Pre-fill daily usage to 90,000 NGN. Key includes the currency segment
	// (issue #189 — daily caps are now tracked per currency, defaulting to
	// NGN).
	today := time.Now().UTC().Format("2006-01-02")
	todayKey := "yc:daily:deposit:test-user-123:NGN:" + today
	rdb.Set(ctx, todayKey, 90_000, 0)
	defer rdb.Del(ctx, todayKey)

	// Requesting 20,000 should exceed 100,000 daily cap
	reqBody, _ := json.Marshal(map[string]any{
		"amountNgn": 20_000,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/wallet/deposit", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	// Verify daily limit error
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "exceeds daily limit")
}

func TestDepositHandler_InitiateWithdraw_BlockedWhenKYCRequired(t *testing.T) {
	ycClient := yellowcard.NewClient("", "", "")
	mockWallet := &mockDepositWalletService{
		wallets: []wallet.Wallet{{PublicKey: "GABC12345"}},
	}

	repo := new(ffmocks.Repository)
	repo.On("List", mock.Anything).Return([]featureflag.FeatureFlag{
		{Flag: "kyc_required", Enabled: true},
	}, nil)
	flagCache := featureflag.NewCache(featureflag.NewService(repo), time.Hour)
	require.NoError(t, flagCache.Refresh(context.Background()))

	h := handler.NewDepositHandler(ycClient, mockWallet).WithFeatureFlags(flagCache)
	r := setupTestDepositRouter(h)

	reqBody, _ := json.Marshal(map[string]any{
		"amountUsdc":    100,
		"bankCode":      "044",
		"accountNumber": "0123456789",
		"accountName":   "Jane Doe",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/wallet/withdraw", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusPreconditionRequired, w.Code)
	assert.Contains(t, w.Body.String(), "kyc_required")
}
