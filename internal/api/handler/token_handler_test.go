package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/token"
)

type mockTokenService struct{ mock.Mock }

func (m *mockTokenService) GetBalance(ctx context.Context, address string) (uint64, error) {
	args := m.Called(ctx, address)
	return args.Get(0).(uint64), args.Error(1)
}

func (m *mockTokenService) Stake(ctx context.Context, userID string, amount uint64) (string, error) {
	args := m.Called(ctx, userID, amount)
	return args.String(0), args.Error(1)
}

func (m *mockTokenService) Unstake(ctx context.Context, userID string, amount uint64) (string, error) {
	args := m.Called(ctx, userID, amount)
	return args.String(0), args.Error(1)
}

func (m *mockTokenService) GetStakedAmount(ctx context.Context, address string) (uint64, error) {
	args := m.Called(ctx, address)
	return args.Get(0).(uint64), args.Error(1)
}

func TestTokenHandler_Stake_WithoutPasskeySeed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := new(mockTokenService)
	svc.On("Stake", mock.Anything, "user-123", uint64(100)).Return("txhash-abc", nil)

	h := handler.NewTokenHandler(svc)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-123")
		c.Next()
	})
	r.POST("/v1/token/stake", h.Stake)

	// No passkeySeed in the body — must not be rejected.
	body, _ := json.Marshal(map[string]any{"amount": 100})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/token/stake", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "txhash-abc")
	assert.NotContains(t, w.Body.String(), "passkeySeed")
	assert.NotContains(t, w.Body.String(), "seed")
	svc.AssertExpectations(t)
}

func TestTokenHandler_Unstake_WithoutPasskeySeed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := new(mockTokenService)
	svc.On("Unstake", mock.Anything, "user-123", uint64(50)).Return("txhash-def", nil)

	h := handler.NewTokenHandler(svc)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-123")
		c.Next()
	})
	r.POST("/v1/token/unstake", h.Unstake)

	body, _ := json.Marshal(map[string]any{"amount": 50})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/token/unstake", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "txhash-def")
	assert.NotContains(t, w.Body.String(), "passkeySeed")
	assert.NotContains(t, w.Body.String(), "seed")
	svc.AssertExpectations(t)
}

func TestTokenHandler_Stake_MissingAmount(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := new(mockTokenService)
	h := handler.NewTokenHandler(svc)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-123")
		c.Next()
	})
	r.POST("/v1/token/stake", h.Stake)

	body, _ := json.Marshal(map[string]any{})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/token/stake", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
	svc.AssertNotCalled(t, "Stake", mock.Anything, mock.Anything, mock.Anything)
}

// TestTokenHandler_Stake_PasskeySeedIgnored ensures that if a client sends
// passkeySeed in the body (backward-compat scenario), the field is silently
// ignored and never echoed back in the response.
func TestTokenHandler_Stake_PasskeySeedIgnored(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := new(mockTokenService)
	svc.On("Stake", mock.Anything, "user-123", uint64(200)).Return("txhash-xyz", nil)

	h := handler.NewTokenHandler(svc)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-123")
		c.Next()
	})
	r.POST("/v1/token/stake", h.Stake)

	// Sending passkeySeed should be ignored, not cause a failure or leak.
	body, _ := json.Marshal(map[string]any{
		"amount":      200,
		"passkeySeed": "PRIVATE_KEY_MATERIAL_MUST_NOT_BE_ECHOED",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/token/stake", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.NotContains(t, w.Body.String(), "passkeySeed")
	assert.NotContains(t, w.Body.String(), "PRIVATE_KEY_MATERIAL_MUST_NOT_BE_ECHOED")
	assert.NotContains(t, w.Body.String(), "seed")
	svc.AssertExpectations(t)
}

// TestTokenHandler_Unstake_PasskeySeedIgnored mirrors the stake test for unstake.
func TestTokenHandler_Unstake_PasskeySeedIgnored(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := new(mockTokenService)
	svc.On("Unstake", mock.Anything, "user-123", uint64(75)).Return("txhash-zzz", nil)

	h := handler.NewTokenHandler(svc)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-123")
		c.Next()
	})
	r.POST("/v1/token/unstake", h.Unstake)

	body, _ := json.Marshal(map[string]any{
		"amount":      75,
		"passkeySeed": "SENSITIVE_SEED_DATA",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/token/unstake", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.NotContains(t, w.Body.String(), "passkeySeed")
	assert.NotContains(t, w.Body.String(), "SENSITIVE_SEED_DATA")
	svc.AssertExpectations(t)
}

var _ token.Service = (*mockTokenService)(nil)
