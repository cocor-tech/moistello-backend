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
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/swap"
)

func setupSwapTestRouter(swapSvc *swap.Service, authUserID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if authUserID != "" {
			c.Set("user_id", authUserID)
		}
		c.Next()
	})

	h := handler.NewSwapHandler(swapSvc)
	r.POST("/v1/swap/offer", h.CreateSwapOffer)
	r.POST("/v1/swap/accept", h.AcceptSwapOffer)
	r.GET("/v1/swap/history", h.GetSwapHistory)

	return r
}

func TestSwapHandler_Unauthorized(t *testing.T) {
	swapSvc := swap.NewService(nil, nil, nil, nil)
	r := setupSwapTestRouter(swapSvc, "") // No authenticated user

	// 1. Create offer without auth -> 401
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/swap/offer", bytes.NewBufferString(`{}`))
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// 2. Accept offer without auth -> 401
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/v1/swap/accept", bytes.NewBufferString(`{}`))
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// 3. History without auth -> 401
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/v1/swap/history", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestSwapHandler_CreateSwapOffer_InvalidBody(t *testing.T) {
	swapSvc := swap.NewService(nil, nil, nil, nil)
	r := setupSwapTestRouter(swapSvc, uuid.NewString())

	// Missing required fields
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/swap/offer", bytes.NewBufferString(`{"circleId":""}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSwapHandler_AcceptSwapOffer_InvalidBody(t *testing.T) {
	swapSvc := swap.NewService(nil, nil, nil, nil)
	r := setupSwapTestRouter(swapSvc, uuid.NewString())

	// Missing swapOfferId
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/swap/accept", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// Mock Repo for Handler Tests
type swapMockRepoForHandler struct {
	offers map[string]*swap.SwapOffer
}

func (m *swapMockRepoForHandler) CreateSwapOffer(ctx context.Context, offer *swap.SwapOffer) error {
	m.offers[offer.ID] = offer
	return nil
}
func (m *swapMockRepoForHandler) GetSwapOfferByID(ctx context.Context, id string) (*swap.SwapOffer, error) {
	return m.offers[id], nil
}
func (m *swapMockRepoForHandler) UpdateSwapOfferStatus(ctx context.Context, id string, status swap.SwapOfferStatus, transactionHash *string) error {
	return nil
}
func (m *swapMockRepoForHandler) CompareAndSwapStatus(ctx context.Context, id string, expectedStatus, newStatus swap.SwapOfferStatus, transactionHash *string) (bool, error) {
	offer, ok := m.offers[id]
	if !ok || offer.Status != expectedStatus {
		return false, nil
	}
	offer.Status = newStatus
	offer.TransactionHash = transactionHash
	return true, nil
}
func (m *swapMockRepoForHandler) ListUserSwapOffers(ctx context.Context, userID string, filter swap.SwapHistoryFilter) ([]swap.SwapOffer, int, error) {
	return []swap.SwapOffer{}, 0, nil
}
func (m *swapMockRepoForHandler) ListCircleSwapOffers(ctx context.Context, circleID string, filter swap.SwapHistoryFilter) ([]swap.SwapOffer, int, error) {
	return nil, 0, nil
}
func (m *swapMockRepoForHandler) CancelExpiredOffers(ctx context.Context) error { return nil }
func (m *swapMockRepoForHandler) ListExpiredCreatedOffers(ctx context.Context, now time.Time) ([]swap.SwapOffer, error) {
	return nil, nil
}

func TestSwapHandler_GetSwapHistory_Success(t *testing.T) {
	repo := &swapMockRepoForHandler{offers: make(map[string]*swap.SwapOffer)}
	swapSvc := swap.NewService(repo, nil, nil, nil)
	userID := uuid.NewString()
	r := setupSwapTestRouter(swapSvc, userID)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/swap/history?limit=10&offset=0", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp["success"].(bool))
}
