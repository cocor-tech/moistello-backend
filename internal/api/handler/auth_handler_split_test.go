package handler_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/auth"
	domainTOTP "github.com/moistello/backend/internal/domain/totp"
	"github.com/moistello/backend/internal/domain/user"
	userMocks "github.com/moistello/backend/internal/domain/user/mocks"
	"github.com/moistello/backend/internal/domain/verification"
	walletDomain "github.com/moistello/backend/internal/domain/wallet"
)

// splitEnv wires the focused auth sub-handlers (login, passkey, totp,
// recovery, sessions, wallet-init) the same way the test suite wires the
// registration flows, with a real (dependency-free) TOTP service so the
// backup-code and TOTP setup flows can be exercised end to end.
type splitEnv struct {
	mockAuthSvc  *mockAuthService
	mockUserRepo *userMocks.Repository
	userSvc      user.Service
	wallet       *mockWalletService
	totpSvc      *domainTOTP.Service
	h            *handler.AuthHandler
}

func newSplitEnv(t *testing.T) *splitEnv {
	t.Helper()
	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo, nil)
	wallet := new(mockWalletService)
	totpSvc := domainTOTP.NewService()

	wallet.On("DeriveWalletSeed", mock.Anything, mock.AnythingOfType("string")).Return(testWalletSeed, nil)
	wallet.On("CreateWallet", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8")).Return(&walletDomain.Wallet{}, nil)

	rdb := redis.NewClient(&redis.Options{Addr: "memory:6379"})
	rdb.AddHook(&memoryRedisHook{store: make(map[string]string)})
	t.Cleanup(func() { _ = rdb.Close() })

	env := &splitEnv{
		mockAuthSvc:  mockAuthSvc,
		mockUserRepo: mockUserRepo,
		userSvc:      userSvc,
		wallet:       wallet,
		totpSvc:      totpSvc,
	}
	env.h = handler.NewAuthHandler(mockAuthSvc, userSvc, wallet, totpSvc, verification.NewService(rdb), nil, nil, mockUserRepo)
	return env
}

func authUser(userID uuid.UUID, email string) *user.User {
	hashed := user.HashEmail(email)
	return &user.User{
		ID:                userID,
		WalletAddress:     emailWalletAddr(email),
		Email:             &hashed,
		EmailVerified:     true,
		Role:              user.RoleUser,
		SessionTTLMinutes: 240,
	}
}

func TestLoginHandler_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	userID := uuid.New()
	hash, err := auth.HashPassword("correct-pass-123")
	assert.NoError(t, err)
	u := authUser(userID, "login@example.com")
	u.PasswordHash.Valid = true
	u.PasswordHash.String = hash
	u.EmailVerified = true

	env.mockUserRepo.On("FindByWalletAddress", mock.Anything, emailWalletAddr("login@example.com")).Return(u, nil)
	env.mockAuthSvc.On("CreateSession", mock.Anything, userID, string(user.RoleUser), mock.Anything, mock.Anything).Return(
		&auth.TokenPair{AccessToken: "at", RefreshToken: "rt", CSRFToken: "ct"}, nil,
	)

	r := gin.New()
	r.POST("/auth/login", env.h.Login)
	body, _ := json.Marshal(map[string]string{"email": "login@example.com", "password": "correct-pass-123"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/login", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "at")
	env.mockAuthSvc.AssertExpectations(t)
}

func TestLoginHandler_IncorrectPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	hash, err := auth.HashPassword("real-password")
	assert.NoError(t, err)
	u := authUser(uuid.New(), "login2@example.com")
	u.PasswordHash = sqlNull(hash)

	env.mockUserRepo.On("FindByWalletAddress", mock.Anything, emailWalletAddr("login2@example.com")).Return(u, nil)

	r := gin.New()
	r.POST("/auth/login", env.h.Login)
	body, _ := json.Marshal(map[string]string{"email": "login2@example.com", "password": "wrong"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/login", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 401, w.Code)
	env.mockAuthSvc.AssertNotCalled(t, "CreateSession", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestLoginHandler_UnverifiedEmailResendsOTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	hash, err := auth.HashPassword("real-password")
	assert.NoError(t, err)
	u := authUser(uuid.New(), "unverified@example.com")
	u.PasswordHash = sqlNull(hash)
	u.EmailVerified = false

	env.mockUserRepo.On("FindByWalletAddress", mock.Anything, emailWalletAddr("unverified@example.com")).Return(u, nil)

	r := gin.New()
	r.POST("/auth/login", env.h.Login)
	body, _ := json.Marshal(map[string]string{"email": "unverified@example.com", "password": "real-password"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/login", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"needsVerification":true`)
	env.mockAuthSvc.AssertNotCalled(t, "CreateSession", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestPasskeyHandler_Nonce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	userID := uuid.New()
	u := authUser(userID, "passkey@example.com")
	env.mockUserRepo.On("FindByPasskeyCredentialID", mock.Anything, "cred-123").Return(u, nil)
	env.mockAuthSvc.On("GenerateNonce", mock.Anything, u.WalletAddress).Return(
		&auth.Nonce{WalletAddress: u.WalletAddress, Nonce: "nonce-xyz"}, nil,
	)

	r := gin.New()
	r.POST("/auth/passkey/nonce", env.h.PasskeyNonce)
	body, _ := json.Marshal(map[string]string{"credentialId": "cred-123"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/passkey/nonce", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "nonce-xyz")
	env.mockAuthSvc.AssertExpectations(t)
}

func TestPasskeyHandler_Verify(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	userID := uuid.New()
	u := authUser(userID, "passkey@example.com")
	env.mockUserRepo.On("FindByPasskeyCredentialID", mock.Anything, "cred-123").Return(u, nil)
	env.mockAuthSvc.On("VerifySignature", mock.Anything, u.WalletAddress, "sig").Return(true, nil)
	env.mockAuthSvc.On("CreateSession", mock.Anything, userID, string(user.RoleUser), mock.Anything, mock.Anything).Return(
		&auth.TokenPair{AccessToken: "at", RefreshToken: "rt", CSRFToken: "ct"}, nil,
	)

	r := gin.New()
	r.POST("/auth/passkey/verify", env.h.PasskeyVerify)
	body, _ := json.Marshal(map[string]string{"credentialId": "cred-123", "signature": "sig"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/passkey/verify", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "at")
	env.mockAuthSvc.AssertExpectations(t)
}

func TestPasskeyHandler_Link(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	userID := uuid.New()
	u := authUser(userID, "passkey@example.com")
	env.mockUserRepo.On("FindByID", mock.Anything, userID).Return(u, nil)
	env.mockUserRepo.On("Update", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", userID.String()); c.Next() })
	r.POST("/auth/passkey/link", env.h.PasskeyLink)
	body, _ := json.Marshal(map[string]string{"credentialId": "cred-456"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/passkey/link", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"success":true`)
	env.mockUserRepo.AssertExpectations(t)
}

func TestTOTPHandler_Setup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	userID := uuid.New()
	u := authUser(userID, "totp@example.com")
	env.mockUserRepo.On("FindByID", mock.Anything, userID).Return(u, nil)
	env.mockUserRepo.On("Update", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", userID.String()); c.Next() })
	r.POST("/auth/totp/setup", env.h.SetupTOTP)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/totp/setup", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "totpSecret")
	assert.Contains(t, w.Body.String(), "totpUri")
	env.mockUserRepo.AssertExpectations(t)
}

func TestTOTPHandler_VerifySetup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Moistello", AccountName: "totp@example.com"})
	assert.NoError(t, err)
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	assert.NoError(t, err)

	userID := uuid.New()
	u := authUser(userID, "totp@example.com")
	u.TOTPSecret = sqlNull(key.Secret())
	env.mockUserRepo.On("FindByID", mock.Anything, userID).Return(u, nil)

	var updated *user.User
	env.mockUserRepo.On("Update", mock.Anything, mock.AnythingOfType("*user.User")).
		Run(func(args mock.Arguments) { updated = args.Get(1).(*user.User) }).Return(nil)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", userID.String()); c.Next() })
	r.POST("/auth/totp/verify", env.h.VerifyTOTPSetup)
	body, _ := json.Marshal(map[string]string{"totpCode": code})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/totp/verify", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code, w.Body.String())
	assert.True(t, updated.TOTPEnabled)
	assert.Len(t, updated.BackupCodes, 10)
	env.mockUserRepo.AssertExpectations(t)
}

func TestRecoveryHandler_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	codes, err := env.totpSvc.GenerateBackupCodes()
	assert.NoError(t, err)
	hashed := env.totpSvc.HashBackupCodes(codes)

	userID := uuid.New()
	u := authUser(userID, "recovery@example.com")
	u.BackupCodes = hashed

	env.mockUserRepo.On("FindByWalletAddress", mock.Anything, emailWalletAddr("recovery@example.com")).Return(u, nil)
	env.mockUserRepo.On("Update", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)
	env.mockAuthSvc.On("CreateSession", mock.Anything, userID, string(user.RoleUser), mock.Anything, mock.Anything).Return(
		&auth.TokenPair{AccessToken: "at", RefreshToken: "rt", CSRFToken: "ct"}, nil,
	)

	r := gin.New()
	r.POST("/auth/recovery", env.h.Recovery)
	body, _ := json.Marshal(map[string]string{"email": "recovery@example.com", "backupCode": codes[0].Plain})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/recovery", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "at")
	env.mockAuthSvc.AssertExpectations(t)
	env.mockUserRepo.AssertExpectations(t)
}

func TestRecoveryHandler_InvalidCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	codes, err := env.totpSvc.GenerateBackupCodes()
	assert.NoError(t, err)

	userID := uuid.New()
	u := authUser(userID, "recovery@example.com")
	u.BackupCodes = env.totpSvc.HashBackupCodes(codes)

	env.mockUserRepo.On("FindByWalletAddress", mock.Anything, emailWalletAddr("recovery@example.com")).Return(u, nil)

	r := gin.New()
	r.POST("/auth/recovery", env.h.Recovery)
	body, _ := json.Marshal(map[string]string{"email": "recovery@example.com", "backupCode": "XXXX-XXXX-XXXX"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/recovery", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
	env.mockAuthSvc.AssertNotCalled(t, "CreateSession", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestSessionHandler_ListSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	userID := uuid.New()
	env.mockAuthSvc.On("ListSessions", mock.Anything, userID.String(), mock.Anything).Return(
		[]auth.SessionInfo{{ID: "abc", DeviceInfo: "linux", IsCurrent: true}}, nil,
	)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", userID.String()); c.Next() })
	r.GET("/sessions", env.h.ListSessions)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/sessions", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "abc")
	env.mockAuthSvc.AssertExpectations(t)
}

func TestSessionHandler_RevokeAllSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	userID := uuid.New()
	env.mockAuthSvc.On("RevokeAllSessions", mock.Anything, userID.String(), mock.Anything).Return(nil)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", userID.String()); c.Next() })
	r.DELETE("/sessions", env.h.RevokeAllSessions)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/sessions", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code, w.Body.String())
	env.mockAuthSvc.AssertExpectations(t)
}

func TestWalletInitHandler_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newSplitEnv(t)

	userID := uuid.New()
	u := authUser(userID, "wallet@example.com")
	env.mockUserRepo.On("FindByID", mock.Anything, userID).Return(u, nil)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", userID.String()); c.Next() })
	r.POST("/auth/wallet/init", env.h.InitWallet)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/wallet/init", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 201, w.Code, w.Body.String())
	env.wallet.AssertExpectations(t)
	env.mockUserRepo.AssertExpectations(t)
}

func sqlNull(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}
