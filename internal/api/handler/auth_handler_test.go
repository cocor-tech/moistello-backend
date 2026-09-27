package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/auth"
	domainJWT "github.com/moistello/backend/internal/domain/auth/jwt"
	"github.com/moistello/backend/internal/domain/user"
	userMocks "github.com/moistello/backend/internal/domain/user/mocks"
)

type mockAuthService struct {
	mock.Mock
}

func (m *mockAuthService) GenerateNonce(ctx context.Context, walletAddress string) (*auth.Nonce, error) {
	args := m.Called(ctx, walletAddress)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.Nonce), args.Error(1)
}

func (m *mockAuthService) VerifySignature(ctx context.Context, walletAddress, signature string) (bool, error) {
	args := m.Called(ctx, walletAddress, signature)
	return args.Bool(0), args.Error(1)
}

func (m *mockAuthService) CreateSession(ctx context.Context, userID uuid.UUID, role string, sessionTTL time.Duration, deviceInfo string) (*auth.TokenPair, error) {
	args := m.Called(ctx, userID, role, sessionTTL, deviceInfo)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.TokenPair), args.Error(1)
}

func (m *mockAuthService) GenerateJWTWithTTL(userID uuid.UUID, walletAddress, role string, ttl time.Duration) (string, error) {
	args := m.Called(userID, walletAddress, role, ttl)
	return args.String(0), args.Error(1)
}

func (m *mockAuthService) ListSessions(ctx context.Context, userID string, currentTokenHash string) ([]auth.SessionInfo, error) {
	args := m.Called(ctx, userID, currentTokenHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]auth.SessionInfo), args.Error(1)
}

func (m *mockAuthService) RevokeSession(ctx context.Context, userID, sessionHash string) error {
	return m.Called(ctx, userID, sessionHash).Error(0)
}

func (m *mockAuthService) RevokeAllSessions(ctx context.Context, userID, currentHash string) error {
	return m.Called(ctx, userID, currentHash).Error(0)
}

func (m *mockAuthService) ValidateSession(ctx context.Context, refreshToken string) (*uuid.UUID, error) {
	args := m.Called(ctx, refreshToken)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*uuid.UUID), args.Error(1)
}

func (m *mockAuthService) GenerateJWT(userID uuid.UUID, walletAddress, role string) (string, error) {
	args := m.Called(userID, walletAddress, role)
	return args.String(0), args.Error(1)
}

func (m *mockAuthService) ValidateJWT(tokenString string) (*auth.JWTCustomClaims, error) {
	args := m.Called(tokenString)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.JWTCustomClaims), args.Error(1)
}

func (m *mockAuthService) RefreshToken(ctx context.Context, refreshToken string) (*auth.TokenPair, error) {
	args := m.Called(ctx, refreshToken)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.TokenPair), args.Error(1)
}

func (m *mockAuthService) RotateSigningKey(newPrivateKeyPEM, newPublicKeyPEM string, newKID string) error {
	return m.Called(newPrivateKeyPEM, newPublicKeyPEM, newKID).Error(0)
}

func (m *mockAuthService) JWTService() domainJWT.Service {
	if args := m.Called(); args.Get(0) != nil {
		return args.Get(0).(domainJWT.Service)
	}
	return nil
}

func TestAuthHandler_Nonce(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo, nil)

	nonceResp := &auth.Nonce{
		WalletAddress: "GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC",
		Nonce:         "nonce-123",
	}
	mockAuthSvc.On("GenerateNonce", mock.Anything, "GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC").Return(nonceResp, nil)

	h := handler.NewAuthHandler(mockAuthSvc, userSvc, nil, nil, nil, nil, nil, nil)
	r := gin.New()
	r.POST("/auth/nonce", h.Nonce)

	body, _ := json.Marshal(map[string]string{"walletAddress": "GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/nonce", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "nonce-123")
	mockAuthSvc.AssertExpectations(t)
}

func TestAuthHandler_Nonce_MissingWallet(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo, nil)

	mockAuthSvc.On("GenerateNonce", mock.Anything, mock.Anything).Return(nil, errors.New("missing wallet"))

	h := handler.NewAuthHandler(mockAuthSvc, userSvc, nil, nil, nil, nil, nil, nil)
	r := gin.New()
	r.POST("/auth/nonce", h.Nonce)

	body, _ := json.Marshal(map[string]string{})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/nonce", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.False(t, w.Code == 200)
}

func TestAuthHandler_Nonce_ServiceError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo, nil)

	mockAuthSvc.On("GenerateNonce", mock.Anything, "GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC").Return(nil, errors.New("redis error"))

	h := handler.NewAuthHandler(mockAuthSvc, userSvc, nil, nil, nil, nil, nil, nil)
	r := gin.New()
	r.POST("/auth/nonce", h.Nonce)

	body, _ := json.Marshal(map[string]string{"walletAddress": "GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/nonce", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 500, w.Code)
	mockAuthSvc.AssertExpectations(t)
}

func TestAuthHandler_Refresh_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo, nil)

	mockAuthSvc.On("RefreshToken", mock.Anything, "refresh-token").Return(
		&auth.TokenPair{AccessToken: "new-jwt", RefreshToken: "new-refresh"}, nil,
	)

	h := handler.NewAuthHandler(mockAuthSvc, userSvc, nil, nil, nil, nil, nil, nil)
	r := gin.New()
	r.POST("/auth/refresh", h.Refresh)

	body, _ := json.Marshal(map[string]string{"refreshToken": "refresh-token"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/refresh", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "new-jwt")
	mockAuthSvc.AssertExpectations(t)
}

func TestAuthHandler_Refresh_Invalid(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo, nil)

	mockAuthSvc.On("RefreshToken", mock.Anything, "bad-token").Return(nil, errors.New("invalid"))

	h := handler.NewAuthHandler(mockAuthSvc, userSvc, nil, nil, nil, nil, nil, nil)
	r := gin.New()
	r.POST("/auth/refresh", h.Refresh)

	body, _ := json.Marshal(map[string]string{"refreshToken": "bad-token"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/refresh", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
	mockAuthSvc.AssertExpectations(t)
}

func TestAuthHandler_Me_UserFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo, nil)

	uid := uuid.New()
	expectedUser := &user.User{
		ID:            uid,
		WalletAddress: "GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC",
		Role:          user.RoleUser,
	}
	mockUserRepo.On("FindByID", mock.Anything, uid).Return(expectedUser, nil)

	h := handler.NewAuthHandler(mockAuthSvc, userSvc, nil, nil, nil, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", uid.String())
		c.Next()
	})
	r.GET("/auth/me", h.Me)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/auth/me", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC")
	mockUserRepo.AssertExpectations(t)
}

func TestAuthHandler_Logout(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo, nil)

	h := handler.NewAuthHandler(mockAuthSvc, userSvc, nil, nil, nil, nil, nil, nil)
	r := gin.New()
	r.POST("/auth/logout", h.Logout)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uuid.New().String(),
		"exp": float64(time.Now().Add(15 * time.Minute).Unix()),
	})
	tokenStr, _ := token.SignedString([]byte("test-secret"))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
}
