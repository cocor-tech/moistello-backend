package handler_test

// Tests for InviteHandler — covers issues #837 (CreateInvite), #838 (ListInvites),
// #839 (RevokeInvite), and #840 (InitiateOfframp).
//
// All four functions were at 0% coverage.  These table-driven tests exercise
// the happy path and at least one error/edge path for each handler, following
// the same fakeService + setupRouter pattern used by mobilemoney_handler_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/invite"
	"github.com/moistello/backend/internal/domain/mobilemoney"
	"github.com/moistello/backend/internal/domain/wallet"
)

// ─── Invite fake service ─────────────────────────────────────────────────────

type fakeInviteService struct {
	generateFn func(ctx context.Context, input invite.GenerateInput) (*invite.Invite, error)
	listFn     func(ctx context.Context, circleID string) ([]invite.Invite, error)
	revokeFn   func(ctx context.Context, code, userID string) error
}

func (f *fakeInviteService) Generate(ctx context.Context, input invite.GenerateInput) (*invite.Invite, error) {
	return f.generateFn(ctx, input)
}
func (f *fakeInviteService) List(ctx context.Context, circleID string) ([]invite.Invite, error) {
	return f.listFn(ctx, circleID)
}
func (f *fakeInviteService) Revoke(ctx context.Context, code, userID string) error {
	return f.revokeFn(ctx, code, userID)
}

// Validate and Accept satisfy invite.Service. These handler tests do not
// exercise the redeem flow, so they are no-ops here.
func (f *fakeInviteService) Validate(context.Context, string) (*invite.Invite, error) {
	return nil, nil
}
func (f *fakeInviteService) Accept(context.Context, string, string) (*invite.Invite, error) {
	return nil, nil
}

// ─── Router helper ───────────────────────────────────────────────────────────

func setupInviteRouter(svc invite.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := handler.NewInviteHandler(svc)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	r.POST("/v1/circles/:id/invites", h.CreateInvite)
	r.GET("/v1/circles/:id/invites", h.ListInvites)
	r.DELETE("/v1/invites/:code", h.RevokeInvite)
	return r
}

// ─── #837 CreateInvite ───────────────────────────────────────────────────────

func TestInviteHandler_CreateInvite_Success(t *testing.T) {
	svc := &fakeInviteService{
		generateFn: func(ctx context.Context, input invite.GenerateInput) (*invite.Invite, error) {
			assert.Equal(t, "circle-1", input.CircleID)
			assert.Equal(t, "user-1", input.UserID)
			return &invite.Invite{Code: "TESTCODE"}, nil
		},
	}
	r := setupInviteRouter(svc)

	body, _ := json.Marshal(map[string]any{"maxUses": 5, "expiresInDays": 7})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v1/circles/circle-1/invites", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), "TESTCODE")
}

func TestInviteHandler_CreateInvite_ServiceError(t *testing.T) {
	svc := &fakeInviteService{
		generateFn: func(_ context.Context, _ invite.GenerateInput) (*invite.Invite, error) {
			return nil, errors.New("db unavailable")
		},
	}
	r := setupInviteRouter(svc)

	body, _ := json.Marshal(map[string]any{})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v1/circles/circle-1/invites", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "failed to create invite")
}

func TestInviteHandler_CreateInvite_InvalidBody(t *testing.T) {
	svc := &fakeInviteService{}
	r := setupInviteRouter(svc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v1/circles/circle-1/invites", bytes.NewReader([]byte("not-json")))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── #838 ListInvites ────────────────────────────────────────────────────────

func TestInviteHandler_ListInvites_Success(t *testing.T) {
	svc := &fakeInviteService{
		listFn: func(ctx context.Context, circleID string) ([]invite.Invite, error) {
			assert.Equal(t, "circle-2", circleID)
			return []invite.Invite{{Code: "ABC"}, {Code: "DEF"}}, nil
		},
	}
	r := setupInviteRouter(svc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v1/circles/circle-2/invites", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "ABC")
	assert.Contains(t, w.Body.String(), "DEF")
}

func TestInviteHandler_ListInvites_ServiceError(t *testing.T) {
	svc := &fakeInviteService{
		listFn: func(_ context.Context, _ string) ([]invite.Invite, error) {
			return nil, errors.New("storage error")
		},
	}
	r := setupInviteRouter(svc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v1/circles/circle-1/invites", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "failed to list invites")
}

func TestInviteHandler_ListInvites_Empty(t *testing.T) {
	svc := &fakeInviteService{
		listFn: func(_ context.Context, _ string) ([]invite.Invite, error) {
			return []invite.Invite{}, nil
		},
	}
	r := setupInviteRouter(svc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v1/circles/circle-3/invites", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "invites")
}

// ─── #839 RevokeInvite ───────────────────────────────────────────────────────

func TestInviteHandler_RevokeInvite_Success(t *testing.T) {
	svc := &fakeInviteService{
		revokeFn: func(ctx context.Context, code, userID string) error {
			assert.Equal(t, "TESTCODE", code)
			assert.Equal(t, "user-1", userID)
			return nil
		},
	}
	r := setupInviteRouter(svc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/v1/invites/TESTCODE", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "true")
}

func TestInviteHandler_RevokeInvite_ServiceError(t *testing.T) {
	svc := &fakeInviteService{
		revokeFn: func(_ context.Context, _, _ string) error {
			return errors.New("not found or not owner")
		},
	}
	r := setupInviteRouter(svc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/v1/invites/BADCODE", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not found or not owner")
}

// ─── #840 InitiateOfframp ────────────────────────────────────────────────────
// The existing mobilemoney_handler_test.go has a stub InitiateOfframp that
// always returns nil — these tests target the real handler path via a richer fake.

type fakeMMServiceFull struct {
	offrampFn func(ctx context.Context, userID string, req mobilemoney.OfframpRequest, key string) (*mobilemoney.Transaction, error)
}

func (f *fakeMMServiceFull) InitiateOnramp(_ context.Context, _ string, _ mobilemoney.OnrampRequest, _ string) (*mobilemoney.Transaction, error) {
	return nil, nil
}
func (f *fakeMMServiceFull) InitiateOfframp(ctx context.Context, userID string, req mobilemoney.OfframpRequest, key string) (*mobilemoney.Transaction, error) {
	return f.offrampFn(ctx, userID, req, key)
}
func (f *fakeMMServiceFull) GetTransaction(_ context.Context, _ string) (*mobilemoney.Transaction, error) {
	return nil, nil
}
func (f *fakeMMServiceFull) Reconcile(_ context.Context) (int, error) { return 0, nil }
func (f *fakeMMServiceFull) ListProviders(_ context.Context) []mobilemoney.ProviderInfo {
	return nil
}
func (f *fakeMMServiceFull) GetSupportedCurrencies(_ context.Context) []string { return nil }

func setupOfframpRouter(svc mobilemoney.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := handler.NewMobileMoneyHandler(svc, &mockDepositWalletService{wallets: []wallet.Wallet{{PublicKey: "GABC"}}})
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	r.POST("/v1/wallet/mobile-money/offramp", h.InitiateOfframp)
	return r
}

func TestInitiateOfframp_Success(t *testing.T) {
	svc := &fakeMMServiceFull{
		offrampFn: func(_ context.Context, userID string, req mobilemoney.OfframpRequest, key string) (*mobilemoney.Transaction, error) {
			assert.Equal(t, "user-1", userID)
			assert.Equal(t, "offramp-key", key)
			assert.Equal(t, "MTN_MoMo", req.AccountName)
			return &mobilemoney.Transaction{ID: "off-1", ProviderRef: "mtn-ref"}, nil
		},
	}
	r := setupOfframpRouter(svc)

	body, _ := json.Marshal(map[string]any{
		"amount": 50, "currency": "UGX", "phoneNumber": "256700000000",
		"accountName": "MTN_MoMo", "idempotencyKey": "offramp-key",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v1/wallet/mobile-money/offramp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), "mtn-ref")
}

func TestInitiateOfframp_MissingIdempotencyKey(t *testing.T) {
	svc := &fakeMMServiceFull{}
	r := setupOfframpRouter(svc)

	body, _ := json.Marshal(map[string]any{
		"amount": 50, "currency": "UGX", "phoneNumber": "256700000000", "accountName": "MTN_MoMo",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v1/wallet/mobile-money/offramp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "idempotency key")
}

func TestInitiateOfframp_MissingRequiredFields(t *testing.T) {
	svc := &fakeMMServiceFull{}
	r := setupOfframpRouter(svc)

	// accountName missing
	body, _ := json.Marshal(map[string]any{
		"amount": 50, "currency": "UGX", "phoneNumber": "256700000000", "idempotencyKey": "k",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v1/wallet/mobile-money/offramp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "accountName")
}

func TestInitiateOfframp_ServiceError(t *testing.T) {
	svc := &fakeMMServiceFull{
		offrampFn: func(_ context.Context, _ string, _ mobilemoney.OfframpRequest, _ string) (*mobilemoney.Transaction, error) {
			return nil, errors.New("provider unavailable")
		},
	}
	r := setupOfframpRouter(svc)

	body, _ := json.Marshal(map[string]any{
		"amount": 50, "currency": "UGX", "phoneNumber": "256700000000",
		"accountName": "MTN_MoMo", "idempotencyKey": "k2",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v1/wallet/mobile-money/offramp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "provider unavailable")
}
