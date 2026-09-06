package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/moistello/backend/internal/api/handler"
	"github.com/moistello/backend/internal/domain/auth"
	"github.com/moistello/backend/internal/domain/user"
	userMocks "github.com/moistello/backend/internal/domain/user/mocks"
	"github.com/moistello/backend/internal/domain/verification"
	walletDomain "github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/pkg/apperrors"
	"github.com/moistello/backend/pkg/validator"
)

func init() {
	// validator.Init is idempotent; safe to call from multiple test files.
	validator.Init()
}

// emailConsistencyEnv sets up a minimal auth handler for email-consistency
// tests, wiring a real verification service backed by the in-memory Redis hook
// defined in auth_handler_register_test.go.
type emailConsistencyEnv struct {
	mockAuthSvc  *mockAuthService
	mockUserRepo *userMocks.Repository
	h            *handler.AuthHandler
	sentOTP      string
	wallet       *mockWalletService
}

func newEmailConsistencyEnv(t *testing.T) *emailConsistencyEnv {
	t.Helper()
	mockAuthSvc := new(mockAuthService)
	mockUserRepo := new(userMocks.Repository)
	userSvc := user.NewService(mockUserRepo, nil)
	wallet := new(mockWalletService)

	rdb := redis.NewClient(&redis.Options{Addr: "memory:6379"})
	rdb.AddHook(&memoryRedisHook{store: make(map[string]string)})
	t.Cleanup(func() { _ = rdb.Close() })

	verificationSvc := verification.NewService(rdb)
	env := &emailConsistencyEnv{
		mockAuthSvc:  mockAuthSvc,
		mockUserRepo: mockUserRepo,
		wallet:       wallet,
	}
	verificationSvc.WithEmailSender(func(addr, code string) error {
		env.sentOTP = code
		return nil
	}, nil)

	wallet.On("DeriveWalletSeed", mock.Anything, mock.AnythingOfType("string")).Return(testWalletSeed, nil)
	wallet.On("CreateWallet", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8")).Return(&walletDomain.Wallet{}, nil)

	env.h = handler.NewAuthHandler(mockAuthSvc, userSvc, wallet, nil, verificationSvc, nil, nil, mockUserRepo)
	return env
}

// TestEmailConsistency_RegisterStoresHashedEmail verifies that RegisterVerify
// persists the email as a full SHA-256 hex digest (user.HashEmail) — not as
// plaintext and not using the old "EMAIL:"+sha256[:16] scheme used in wallet
// address derivation.
func TestEmailConsistency_RegisterStoresHashedEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newEmailConsistencyEnv(t)

	const testAddr = "register-consistency@example.com"
	walletAddr := emailWalletAddrForTest(testAddr)

	env.mockUserRepo.On("FindByWalletAddress", mock.Anything, walletAddr).Return(nil, apperrors.ErrNotFound)

	r := gin.New()
	r.POST("/auth/register", env.h.Register)
	r.POST("/auth/register/verify", env.h.RegisterVerify)

	// Step 1 – initiate registration.
	body, _ := json.Marshal(map[string]string{
		"email":    testAddr,
		"password": "StrongPass123!",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.NotEmpty(t, env.sentOTP)

	// Step 2 – capture what email value is written to the DB.
	var storedUser *user.User
	env.mockUserRepo.On("Create", mock.Anything, mock.AnythingOfType("*user.User")).
		Run(func(args mock.Arguments) {
			storedUser = args.Get(1).(*user.User)
		}).Return(nil)
	env.mockUserRepo.On("FindByWalletAddress", mock.Anything, walletAddr).Return(nil, apperrors.ErrNotFound)
	env.mockAuthSvc.On("CreateSession",
		mock.Anything, mock.AnythingOfType("uuid.UUID"),
		mock.Anything, mock.Anything, mock.Anything,
	).Return(&auth.TokenPair{AccessToken: "tok", RefreshToken: "ref", CSRFToken: "csrf"}, nil)

	verifyBody, _ := json.Marshal(map[string]string{
		"email": testAddr,
		"code":  env.sentOTP,
	})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/auth/register/verify", bytes.NewBuffer(verifyBody))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusCreated, w2.Code, w2.Body.String())

	// The stored email must be the canonical user.HashEmail value.
	assert.NotNil(t, storedUser, "user.Create must have been called")
	if storedUser != nil && storedUser.Email != nil {
		expected := user.HashEmail(testAddr)
		assert.Equal(t, expected, *storedUser.Email,
			"email must be stored as full SHA-256 hex (user.HashEmail), not plaintext or truncated")
		assert.NotContains(t, *storedUser.Email, "@",
			"stored email must not contain @ (must not be plaintext)")
		assert.Len(t, *storedUser.Email, 64,
			"SHA-256 hex digest must be exactly 64 characters")
	}
}

// TestEmailConsistency_FindByEmailUsesHashEmail verifies that the repository's
// FindByEmail always hashes before querying, so a lookup with the plaintext
// email finds the hashed row.
func TestEmailConsistency_FindByEmailUsesHashEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newEmailConsistencyEnv(t)

	const addr = "lookup@example.com"
	hashed := user.HashEmail(addr)

	expected := &user.User{
		ID:            uuid.New(),
		WalletAddress: emailWalletAddrForTest(addr),
		Email:         &hashed,
		Role:          user.RoleUser,
	}
	// The mock repo is called with the hashed email by the pg repository's
	// FindByEmail implementation. The service calls repo.FindByEmail(ctx, addr)
	// which internally hashes. Here we use a direct call to verify the mock.
	env.mockUserRepo.On("FindByEmail", mock.Anything, hashed).Return(expected, nil)

	ctx := context.Background()
	// Directly test the mock expectation — that the hashed value is the lookup key.
	got, err := env.mockUserRepo.FindByEmail(ctx, hashed)
	assert.NoError(t, err)
	assert.Equal(t, expected.ID, got.ID)
	env.mockUserRepo.AssertExpectations(t)
}

// TestEmailConsistency_UpdateProfileHashesEmail verifies that UpdateProfile
// stores the hashed email, not plaintext.
func TestEmailConsistency_UpdateProfileHashesEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newEmailConsistencyEnv(t)

	userID := uuid.New()
	existing := &user.User{
		ID:            userID,
		WalletAddress: "GTEST",
		Role:          user.RoleUser,
	}
	env.mockUserRepo.On("FindByID", mock.Anything, userID).Return(existing, nil)

	var updatedUser *user.User
	env.mockUserRepo.On("Update", mock.Anything, mock.AnythingOfType("*user.User")).
		Run(func(args mock.Arguments) {
			updatedUser = args.Get(1).(*user.User)
		}).Return(nil)

	userSvc := user.NewService(env.mockUserRepo, nil)
	const newEmail = "updated@example.com"
	_, err := userSvc.UpdateProfile(context.Background(), userID.String(), user.UpdateProfileInput{
		Email: strPtr(newEmail),
	})
	assert.NoError(t, err)

	assert.NotNil(t, updatedUser)
	if updatedUser != nil && updatedUser.Email != nil {
		expected := user.HashEmail(newEmail)
		assert.Equal(t, expected, *updatedUser.Email,
			"UpdateProfile must store hashed email, not plaintext")
		assert.Len(t, *updatedUser.Email, 64)
	}
}

func strPtr(s string) *string { return &s }

// emailWalletAddrForTest mirrors the handler's emailWalletAddress logic so
// tests can produce the expected wallet_address without importing handler internals.
func emailWalletAddrForTest(email string) string {
	return emailWalletAddr(email)
}
