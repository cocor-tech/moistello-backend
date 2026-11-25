package token

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stellar/go/keypair"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/pkg/crypto"
)

type mockWalletRepoForToken struct {
	wallets map[string]*wallet.Wallet
}

func newMockWalletRepoForToken() *mockWalletRepoForToken {
	return &mockWalletRepoForToken{
		wallets: make(map[string]*wallet.Wallet),
	}
}

func (m *mockWalletRepoForToken) Create(ctx context.Context, w *wallet.Wallet) error {
	m.wallets[w.ID] = w
	return nil
}

func (m *mockWalletRepoForToken) FindByID(ctx context.Context, id string) (*wallet.Wallet, error) {
	w, ok := m.wallets[id]
	if !ok {
		return nil, errors.New("wallet not found")
	}
	return w, nil
}

func (m *mockWalletRepoForToken) FindByUserID(ctx context.Context, userID string) ([]wallet.Wallet, error) {
	var result []wallet.Wallet
	for _, w := range m.wallets {
		if w.UserID == userID {
			result = append(result, *w)
		}
	}
	return result, nil
}

func (m *mockWalletRepoForToken) FindByPublicKey(ctx context.Context, publicKey string) (*wallet.Wallet, error) {
	for _, w := range m.wallets {
		if w.PublicKey == publicKey {
			return w, nil
		}
	}
	return nil, errors.New("wallet not found")
}

func (m *mockWalletRepoForToken) Delete(ctx context.Context, id string) error { return nil }
func (m *mockWalletRepoForToken) DeleteByOwner(ctx context.Context, walletID, userID string) error {
	return nil
}
func (m *mockWalletRepoForToken) CheckRateLimit(ctx context.Context, userID uuid.UUID) (bool, error) {
	return true, nil
}
func (m *mockWalletRepoForToken) IncrementRateLimit(ctx context.Context, userID uuid.UUID) error {
	return nil
}
func (m *mockWalletRepoForToken) GetDailySpending(ctx context.Context, userID uuid.UUID) (float64, error) {
	return 0, nil
}
func (m *mockWalletRepoForToken) RecordWithdrawalAudit(ctx context.Context, r *wallet.WithdrawalRecord) error {
	return nil
}

// ─── Token Service Unit Tests ─────────────────────────────────────────────────

func TestTokenService_NewService_MissingContractID(t *testing.T) {
	repo := newMockWalletRepoForToken()
	_, err := NewService(repo, Config{
		GovernanceTokenContractID: "",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "governance token contract ID is required")
}

func TestTokenService_NewService_EncryptionKey(t *testing.T) {
	repo := newMockWalletRepoForToken()

	// Invalid encryption key
	_, err := NewService(repo, Config{
		GovernanceTokenContractID: "CCONTACT...",
		EncryptionKey:             "invalid-hex-key",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing encryption key")

	// Valid 32-byte hex key
	validKey := hex.EncodeToString(make([]byte, 32))
	svc, err := NewService(repo, Config{
		GovernanceTokenContractID: "CCONTACT...",
		EncryptionKey:             validKey,
	})
	assert.NoError(t, err)
	assert.NotNil(t, svc)
}

func TestTokenService_Stake_WalletNotFound(t *testing.T) {
	repo := newMockWalletRepoForToken()
	svc, err := NewService(repo, Config{
		GovernanceTokenContractID: "CCONTACT...",
		SorobanRPCURL:             "http://localhost:8000",
	})
	require.NoError(t, err)

	_, err = svc.Stake(context.Background(), uuid.NewString(), 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "user wallet not found")
}

func TestTokenService_Unstake_WalletNotFound(t *testing.T) {
	repo := newMockWalletRepoForToken()
	svc, err := NewService(repo, Config{
		GovernanceTokenContractID: "CCONTACT...",
		SorobanRPCURL:             "http://localhost:8000",
	})
	require.NoError(t, err)

	_, err = svc.Unstake(context.Background(), uuid.NewString(), 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "user wallet not found")
}

func TestTokenService_Stake_DecryptSecretFailure(t *testing.T) {
	repo := newMockWalletRepoForToken()
	userID := uuid.NewString()
	repo.wallets["w1"] = &wallet.Wallet{
		ID:                 "w1",
		UserID:             userID,
		EncryptedSecretKey: []byte("corrupted-secret"),
		EncryptionNonce:    []byte("bad-nonce"),
	}

	svc, err := NewService(repo, Config{
		GovernanceTokenContractID: "CCONTACT...",
		SorobanRPCURL:             "http://localhost:8000",
	})
	require.NoError(t, err)

	_, err = svc.Stake(context.Background(), userID, 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decrypting wallet secret")
}

func TestTokenService_Stake_InvalidSigner(t *testing.T) {
	repo := newMockWalletRepoForToken()
	userID := uuid.NewString()
	encKey := make([]byte, 32)
	_, _ = rand.Read(encKey)

	// Encrypt an invalid secret (not a Stellar secret key)
	ciphertext, nonce, err := crypto.Encrypt([]byte("not-a-valid-stellar-seed"), encKey)
	require.NoError(t, err)

	repo.wallets["w1"] = &wallet.Wallet{
		ID:                 "w1",
		UserID:             userID,
		EncryptedSecretKey: ciphertext,
		EncryptionNonce:    nonce,
	}

	svc, err := NewService(repo, Config{
		GovernanceTokenContractID: "CCONTACT...",
		EncryptionKey:             hex.EncodeToString(encKey),
		SorobanRPCURL:             "http://localhost:8000",
	})
	require.NoError(t, err)

	_, err = svc.Stake(context.Background(), userID, 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating signer")
}

func TestTokenService_Stake_InvokeFailure(t *testing.T) {
	repo := newMockWalletRepoForToken()
	userID := uuid.NewString()
	encKey := make([]byte, 32)
	_, _ = rand.Read(encKey)

	kp, err := keypair.Random()
	require.NoError(t, err)

	ciphertext, nonce, err := crypto.Encrypt([]byte(kp.Seed()), encKey)
	require.NoError(t, err)

	repo.wallets["w1"] = &wallet.Wallet{
		ID:                 "w1",
		UserID:             userID,
		EncryptedSecretKey: ciphertext,
		EncryptionNonce:    nonce,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "simulation failure", http.StatusInternalServerError)
	}))
	defer server.Close()

	svc, err := NewService(repo, Config{
		GovernanceTokenContractID: "CCONTACT...",
		EncryptionKey:             hex.EncodeToString(encKey),
		SorobanRPCURL:             server.URL,
		HorizonURL:                server.URL,
	})
	require.NoError(t, err)

	_, err = svc.Stake(context.Background(), userID, 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "executing stake")

	_, err = svc.Unstake(context.Background(), userID, 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "executing unstake")
}

// ─── Read Contract Simulation Tests ──────────────────────────────────────────

func TestTokenService_GetBalance_And_GetStakedAmount(t *testing.T) {
	repo := newMockWalletRepoForToken()

	tests := []struct {
		name          string
		responseJSON  string
		statusCode    int
		expectedError string
		expectedValue uint64
	}{
		{
			name:          "success",
			statusCode:    http.StatusOK,
			responseJSON:  `{"jsonrpc":"2.0","id":1,"result":{"returnValue":{"u64":"15000"}}}`,
			expectedValue: 15000,
		},
		{
			name:          "rpc simulation error",
			statusCode:    http.StatusInternalServerError,
			responseJSON:  `server error`,
			expectedError: "simulating",
		},
		{
			name:          "contract error",
			statusCode:    http.StatusOK,
			responseJSON:  `{"jsonrpc":"2.0","id":1,"result":{"error":"contract trapped"}}`,
			expectedError: "contract error: contract trapped",
		},
		{
			name:          "no return value",
			statusCode:    http.StatusOK,
			responseJSON:  `{"jsonrpc":"2.0","id":1,"result":{}}`,
			expectedError: "returned no value",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.responseJSON))
			}))
			defer server.Close()

			svc, err := NewService(repo, Config{
				GovernanceTokenContractID: "CCONTACT...",
				SorobanRPCURL:             server.URL,
				HorizonURL:                server.URL,
			})
			require.NoError(t, err)

			val, err := svc.GetBalance(context.Background(), "GANYADDRESS")
			if tc.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedValue, val)
			}

			val, err = svc.GetStakedAmount(context.Background(), "GANYADDRESS")
			if tc.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedValue, val)
			}
		})
	}
}

// ─── scvalToUint64 Conversion Tests ──────────────────────────────────────────

func TestSCValToUint64(t *testing.T) {
	// 1. u64
	val, err := scvalToUint64(map[string]any{"u64": "1234567890"})
	require.NoError(t, err)
	assert.Equal(t, uint64(1234567890), val)

	// 2. u32
	val, err = scvalToUint64(map[string]any{"u32": "42"})
	require.NoError(t, err)
	assert.Equal(t, uint64(42), val)

	// 3. i64
	val, err = scvalToUint64(map[string]any{"i64": "999"})
	require.NoError(t, err)
	assert.Equal(t, uint64(999), val)

	// 4. i32
	val, err = scvalToUint64(map[string]any{"i32": "100"})
	require.NoError(t, err)
	assert.Equal(t, uint64(100), val)

	// 5. u128 with hi=0
	val, err = scvalToUint64(map[string]any{"u128": map[string]any{"hi": "0", "lo": "500"}})
	require.NoError(t, err)
	assert.Equal(t, uint64(500), val)

	// 6. i128 with hi=0
	val, err = scvalToUint64(map[string]any{"i128": map[string]any{"hi": "0", "lo": "700"}})
	require.NoError(t, err)
	assert.Equal(t, uint64(700), val)

	// 7. u128 with hi > 0 (overflows uint64)
	_, err = scvalToUint64(map[string]any{"u128": map[string]any{"hi": "1", "lo": "500"}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds uint64")

	// 8. Invalid u64 parse
	_, err = scvalToUint64(map[string]any{"u64": "not-a-number"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing u64 return value")

	// 9. u128 invalid hi/lo
	_, err = scvalToUint64(map[string]any{"u128": "not-a-map"})
	assert.Error(t, err)

	_, err = scvalToUint64(map[string]any{"u128": map[string]any{"hi": "bad", "lo": "100"}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "high bits")

	_, err = scvalToUint64(map[string]any{"u128": map[string]any{"hi": "0", "lo": "bad"}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "low bits")

	// 10. Invalid type
	_, err = scvalToUint64("not a map")
	assert.Error(t, err)

	// 11. Missing numeric field
	_, err = scvalToUint64(map[string]any{"str": "hello"})
	assert.Error(t, err)
}
