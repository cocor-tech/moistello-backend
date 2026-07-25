package wallet_test

import (
	"context"
	"testing"

	"wallet"
)

type mockRepo struct {
	wallets map[string]*wallet.Wallet
}

func (m *mockRepo) GetByID(ctx context.Context, walletID string) (*wallet.Wallet, error) {
	w, exists := m.wallets[walletID]
	if !exists {
		return nil, nil
	}
	return w, nil
}

func (m *mockRepo) DeleteByIDAndUserID(ctx context.Context, walletID, userID string) error {
	w, exists := m.wallets[walletID]
	if !exists || w.UserID != userID {
		return wallet.ErrWalletNotFound
	}
	delete(m.wallets, walletID)
	return nil
}

func TestDeleteWallet_Authorization(t *testing.T) {
	ctx := context.Background()
	repo := &mockRepo{
		wallets: map[string]*wallet.Wallet{
			"wallet-123": {ID: "wallet-123", UserID: "owner-user-777"},
		},
	}
	svc := wallet.NewService(repo)

	t.Run("authorized owner can delete wallet", func(t *testing.T) {
		err := svc.DeleteWallet(ctx, "wallet-123", "owner-user-777")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("unauthorized user cannot delete someone else's wallet", func(t *testing.T) {
		// Re-seed wallet
		repo.wallets["wallet-123"] = &wallet.Wallet{ID: "wallet-123", UserID: "owner-user-777"}

		err := svc.DeleteWallet(ctx, "wallet-123", "attacker-user-999")
		if err == nil || !errors.Is(err, wallet.ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized, got %v", err)
		}
	})
}

package wallet_test

import (
	"testing"

	"wallet"
)

func TestNewService_HorizonConfig(t *testing.T) {
	t.Run("successfully configures custom horizon URL", func(t *testing.T) {
		cfg := wallet.Config{
			HorizonURL: "https://horizon.stellar.org", // Mainnet target
		}

		svc, err := wallet.NewService(cfg)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if svc == nil {
			t.Fatal("expected service instance to be non-nil")
		}
	})

	t.Run("fails when horizon URL is unconfigured", func(t *testing.T) {
		cfg := wallet.Config{HorizonURL: ""}

		_, err := wallet.NewService(cfg)
		if err == nil {
			t.Fatal("expected error for empty horizon_url, got nil")
		}
	})
}