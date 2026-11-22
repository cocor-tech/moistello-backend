package wallet

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stellar/go/keypair"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockWalletRepo struct {
	wallets       map[string]*Wallet
	createErr     error
	deleteByOwner error
}

func newMockWalletRepo() *mockWalletRepo {
	return &mockWalletRepo{
		wallets: make(map[string]*Wallet),
	}
}

func (m *mockWalletRepo) Create(ctx context.Context, w *Wallet) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.wallets[w.ID] = w
	return nil
}

func (m *mockWalletRepo) FindByID(ctx context.Context, id string) (*Wallet, error) {
	w, ok := m.wallets[id]
	if !ok {
		return nil, errors.New("wallet not found")
	}
	return w, nil
}

func (m *mockWalletRepo) FindByUserID(ctx context.Context, userID string) ([]Wallet, error) {
	var result []Wallet
	for _, w := range m.wallets {
		if w.UserID == userID {
			result = append(result, *w)
		}
	}
	return result, nil
}

func (m *mockWalletRepo) FindByPublicKey(ctx context.Context, publicKey string) (*Wallet, error) {
	for _, w := range m.wallets {
		if w.PublicKey == publicKey {
			return w, nil
		}
	}
	return nil, errors.New("wallet not found")
}

func (m *mockWalletRepo) Delete(ctx context.Context, id string) error { return nil }
func (m *mockWalletRepo) DeleteByOwner(ctx context.Context, walletID, userID string) error {
	if m.deleteByOwner != nil {
		return m.deleteByOwner
	}
	delete(m.wallets, walletID)
	return nil
}

func (m *mockWalletRepo) CheckRateLimit(ctx context.Context, userID uuid.UUID) (bool, error) {
	return true, nil
}
func (m *mockWalletRepo) IncrementRateLimit(ctx context.Context, userID uuid.UUID) error { return nil }
func (m *mockWalletRepo) GetDailySpending(ctx context.Context, userID uuid.UUID) (float64, error) {
	return 0, nil
}
func (m *mockWalletRepo) RecordWithdrawalAudit(ctx context.Context, r *WithdrawalRecord) error {
	return nil
}

// newTestService builds a wallet service for unit tests.
func newTestService(t *testing.T, cfg Config) Service {
	t.Helper()
	kp, err := keypair.Random()
	require.NoError(t, err)
	if cfg.MasterSecretKey == "" {
		cfg.MasterSecretKey = kp.Seed()
	}
	svc, err := NewService(nil, cfg)
	require.NoError(t, err)
	return svc
}

func TestNewService_InvalidMasterSecret(t *testing.T) {
	_, err := NewService(nil, Config{MasterSecretKey: "invalid-key"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing master secret key")
}

func TestDeriveWalletSeed_MissingPepper(t *testing.T) {
	svc := newTestService(t, Config{})

	seed, err := svc.DeriveWalletSeed(context.Background(), "user@example.com")
	assert.Error(t, err)
	assert.Empty(t, seed)
	assert.Contains(t, err.Error(), "wallet pepper is not configured")
}

func TestDeriveWalletSeed_DeterministicAndUniquePerEmail(t *testing.T) {
	svc := newTestService(t, Config{
		WalletPepper:  "test-secret-pepper-123",
		Argon2Time:    1,
		Argon2Memory:  64 * 1024,
		Argon2Threads: 4,
	})

	seed, err := svc.DeriveWalletSeed(context.Background(), "user@example.com")
	require.NoError(t, err)
	assert.Len(t, seed, 64) // 32-byte key, hex-encoded

	// Deterministic: the same email always derives the same seed.
	seed2, err := svc.DeriveWalletSeed(context.Background(), "user@example.com")
	require.NoError(t, err)
	assert.Equal(t, seed, seed2)

	// Unique per email: the email participates in the salt.
	seedOther, err := svc.DeriveWalletSeed(context.Background(), "other@example.com")
	require.NoError(t, err)
	assert.NotEqual(t, seed, seedOther)
}

func TestDeriveWalletSeed_DifferentPepperChangesSeed(t *testing.T) {
	svcA := newTestService(t, Config{WalletPepper: "pepper-a", Argon2Time: 1})
	svcB := newTestService(t, Config{WalletPepper: "pepper-b", Argon2Time: 1})

	seedA, err := svcA.DeriveWalletSeed(context.Background(), "user@example.com")
	require.NoError(t, err)
	seedB, err := svcB.DeriveWalletSeed(context.Background(), "user@example.com")
	require.NoError(t, err)
	assert.NotEqual(t, seedA, seedB)
}

func TestDeriveWalletSeed_DefaultsWhenParamsZero(t *testing.T) {
	// Zero argon2 params fall back to the conservative defaults rather than
	// failing or producing degenerate output.
	svc := newTestService(t, Config{WalletPepper: "pepper"})

	seed, err := svc.DeriveWalletSeed(context.Background(), "user@example.com")
	require.NoError(t, err)
	assert.Len(t, seed, 64)
}

func TestWalletService_CreateWallet_SuccessAndRepoError(t *testing.T) {
	kp, err := keypair.Random()
	require.NoError(t, err)

	repo := newMockWalletRepo()
	cfg := Config{
		MasterSecretKey: kp.Seed(),
		MinBalanceXLM:   2.5,
	}
	svc, err := NewService(repo, cfg)
	require.NoError(t, err)

	seed := make([]byte, 32)
	_, _ = rand.Read(seed)

	// 1. Success
	w, err := svc.CreateWallet(context.Background(), "user-123", seed)
	require.NoError(t, err)
	assert.NotEmpty(t, w.ID)
	assert.Equal(t, "user-123", w.UserID)
	assert.NotEmpty(t, w.PublicKey)

	// 2. Repo error
	repo.createErr = errors.New("db insert failure")
	_, err = svc.CreateWallet(context.Background(), "user-123", seed)
	assert.Error(t, err)
	assert.Equal(t, "db insert failure", err.Error())
}

func TestWalletService_SimpleMethods(t *testing.T) {
	kp, err := keypair.Random()
	require.NoError(t, err)

	repo := newMockWalletRepo()
	repo.wallets["w1"] = &Wallet{ID: "w1", UserID: "user-1"}

	svc, err := NewService(repo, Config{MasterSecretKey: kp.Seed()})
	require.NoError(t, err)

	// GetWallets
	wallets, err := svc.GetWallets(context.Background(), "user-1")
	require.NoError(t, err)
	assert.Len(t, wallets, 1)

	// GetBalance
	bal, err := svc.GetBalance(context.Background(), "user-1")
	require.NoError(t, err)
	assert.Equal(t, 100.0, bal.XLM)
	assert.Equal(t, 50.0, bal.USDC)

	// SignTransaction
	sig, err := svc.SignTransaction(context.Background(), "w1", []byte("seed"), "xdr")
	require.NoError(t, err)
	assert.Empty(t, sig)

	// SendPayment
	tx, err := svc.SendPayment(context.Background(), "user-1", []byte("seed"), "GDEST", "XLM", 10, "memo", "127.0.0.1", "agent")
	require.NoError(t, err)
	assert.Equal(t, "txhash", tx)

	// DeleteWallet
	err = svc.DeleteWallet(context.Background(), "user-1", "w1")
	require.NoError(t, err)

	// Nil repo coverage
	nilSvc, err := NewService(nil, Config{MasterSecretKey: kp.Seed()})
	require.NoError(t, err)
	wList, err := nilSvc.GetWallets(context.Background(), "user-1")
	assert.NoError(t, err)
	assert.Nil(t, wList)

	err = nilSvc.DeleteWallet(context.Background(), "user-1", "w1")
	assert.NoError(t, err)
}
