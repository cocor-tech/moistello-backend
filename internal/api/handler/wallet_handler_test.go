package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/wallet"
)

func TestWalletHandler_Withdraw_WithoutPasskeySeed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := new(mockWalletService)
	svc.On(
		"SendPayment",
		mock.Anything,
		"user-123",
		mock.AnythingOfType("[]uint8"), // empty seed when omitted
		"GDEST...",
		"XLM",
		float64(10.5),
		"",
		mock.AnythingOfType("string"),
		mock.AnythingOfType("string"),
	).Return("txhash-wallet", nil)

	h := handler.NewWalletHandler(svc)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-123")
		c.Next()
	})
	r.POST("/v1/wallets/withdraw", h.Withdraw)

	// No passkeySeed in the body — must not be rejected.
	body, _ := json.Marshal(map[string]any{
		"destination": "GDEST...",
		"asset":       "XLM",
		"amount":      10.5,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/wallets/withdraw", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "txhash-wallet")
	assert.NotContains(t, w.Body.String(), "passkeySeed")
	svc.AssertExpectations(t)
}

func TestWalletHandler_Withdraw_MemoAndLabel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	post := func(svc *mockWalletService, payload map[string]any) *httptest.ResponseRecorder {
		h := handler.NewWalletHandler(svc)
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("userID", "user-123"); c.Next() })
		r.POST("/v1/wallets/withdraw", h.Withdraw)
		body, _ := json.Marshal(payload)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/wallets/withdraw", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}
	base := func() map[string]any {
		return map[string]any{"destination": "GDEST...", "asset": "XLM", "amount": 1.0}
	}

	t.Run("memo and label are passed through and echoed", func(t *testing.T) {
		svc := new(mockWalletService)
		svc.On("SendPayment", mock.Anything, "user-123", mock.Anything, "GDEST...", "XLM", float64(1),
			"inv-42", mock.Anything, mock.Anything).Return("txhash-memo", nil)
		p := base()
		p["memo"], p["label"] = "inv-42", "  Rent  "

		w := post(svc, p)

		assert.Equal(t, 200, w.Code)
		assert.Contains(t, w.Body.String(), `"memo":"inv-42"`)
		assert.Contains(t, w.Body.String(), `"label":"Rent"`)
		svc.AssertExpectations(t)
	})

	t.Run("over-long memo is rejected before sending", func(t *testing.T) {
		svc := new(mockWalletService)
		p := base()
		p["memo"] = "this memo is far too long for stellar"

		w := post(svc, p)

		assert.Equal(t, 400, w.Code)
		svc.AssertNotCalled(t, "SendPayment")
	})
}

var _ wallet.Service = (*mockWalletService)(nil)
