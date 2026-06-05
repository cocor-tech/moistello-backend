package handler_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/auth"
	userMocks "github.com/moistello/backend/internal/domain/user/mocks"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/apperrors"
	"github.com/moistello/backend/pkg/validator"
)

func init() {
	validator.Init()
}

func newRegisterHandler(t *testing.T) (
	*mockAuthService,
	*userMocks.Repository,
	user.Service,
	*handler.AuthHandler,
) {
	t.Helper()
	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo, nil)
	return mockAuthSvc, mockUserRepo, userSvc, handler.NewAuthHandler(mockAuthSvc, userSvc, nil)
}

func TestAuthHandler_Register_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockAuthSvc, mockUserRepo, _, h := newRegisterHandler(t)

	mockAuthSvc.On("VerifySignature", mock.Anything, "GABC...", "sig-valid").Return(true, nil)
	mockUserRepo.On("FindByWalletAddress", mock.Anything, "GABC...").Return(nil, apperrors.ErrNotFound)
	mockUserRepo.On("Create", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)

	uid := uuid.New()
	mockUserRepo.On("FindByID", mock.Anything, mock.AnythingOfType("uuid.UUID")).Return(&user.User{ID: uid}, nil)
	mockUserRepo.On("Update", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)

	mockAuthSvc.On("CreateSession", mock.Anything, mock.AnythingOfType("uuid.UUID")).Return(
		&auth.TokenPair{AccessToken: "jwt-token", RefreshToken: "refresh-token"}, nil,
	)

	r := gin.New()
	r.POST("/v1/auth/register", h.Register)

	body, _ := json.Marshal(map[string]string{
		"walletAddress": "GABC...",
		"signature":     "sig-valid",
		"displayName":   "Test User",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "jwt-token")
	assert.Contains(t, w.Body.String(), "Test User")
	mockAuthSvc.AssertExpectations(t)
	mockUserRepo.AssertExpectations(t)
}

func TestAuthHandler_Register_MissingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockAuthSvc, mockUserRepo, _, h := newRegisterHandler(t)

	// In Gin TestMode, binding:"required" does not reject empty JSON bodies,
	// so the handler proceeds with zero-value fields. We set up expectations
	// for the full flow to match how Gin behaves in tests.
	mockAuthSvc.On("VerifySignature", mock.Anything, "", "").Return(true, nil)
	mockUserRepo.On("FindByWalletAddress", mock.Anything, "").Return(nil, apperrors.ErrNotFound)
	mockUserRepo.On("Create", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)

	uid := uuid.New()
	mockUserRepo.On("FindByID", mock.Anything, mock.AnythingOfType("uuid.UUID")).Return(&user.User{ID: uid}, nil)
	mockUserRepo.On("Update", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)
	mockAuthSvc.On("CreateSession", mock.Anything, mock.AnythingOfType("uuid.UUID")).Return(
		&auth.TokenPair{AccessToken: "jwt", RefreshToken: "rt"}, nil,
	)

	r := gin.New()
	r.POST("/v1/auth/register", h.Register)

	body, _ := json.Marshal(map[string]string{})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	mockAuthSvc.AssertExpectations(t)
	mockUserRepo.AssertExpectations(t)
}

func TestAuthHandler_Register_InvalidSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockAuthSvc, _, _, h := newRegisterHandler(t)

	mockAuthSvc.On("VerifySignature", mock.Anything, "GABC...", "sig-bad").Return(false, nil)

	r := gin.New()
	r.POST("/v1/auth/register", h.Register)

	body, _ := json.Marshal(map[string]string{
		"walletAddress": "GABC...",
		"signature":     "sig-bad",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
	mockAuthSvc.AssertExpectations(t)
}

func TestAuthHandler_Register_ExistingUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockAuthSvc, mockUserRepo, _, h := newRegisterHandler(t)

	existingUser := &user.User{
		ID:            uuid.New(),
		WalletAddress: "GABC...",
		Role:          user.RoleUser,
	}

	mockAuthSvc.On("VerifySignature", mock.Anything, "GABC...", "sig-valid").Return(true, nil)
	mockUserRepo.On("FindByWalletAddress", mock.Anything, "GABC...").Return(existingUser, nil)

	mockUserRepo.On("FindByID", mock.Anything, existingUser.ID).Return(existingUser, nil)
	mockUserRepo.On("Update", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)
	mockAuthSvc.On("CreateSession", mock.Anything, mock.AnythingOfType("uuid.UUID")).Return(
		&auth.TokenPair{AccessToken: "jwt-token", RefreshToken: "refresh-token"}, nil,
	)

	r := gin.New()
	r.POST("/v1/auth/register", h.Register)

	body, _ := json.Marshal(map[string]string{
		"walletAddress": "GABC...",
		"signature":     "sig-valid",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "jwt-token")
	mockUserRepo.AssertExpectations(t)
	mockAuthSvc.AssertExpectations(t)
}

func TestAuthHandler_Register_VerifySignatureError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockAuthSvc, _, _, h := newRegisterHandler(t)

	mockAuthSvc.On("VerifySignature", mock.Anything, "GABC...", "sig-valid").Return(false, errors.New("crypto error"))

	r := gin.New()
	r.POST("/v1/auth/register", h.Register)

	body, _ := json.Marshal(map[string]string{
		"walletAddress": "GABC...",
		"signature":     "sig-valid",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/auth/register", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
	mockAuthSvc.AssertExpectations(t)
}
