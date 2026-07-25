package wallet

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrWalletNotFound = errors.New("wallet not found")
	ErrUnauthorized   = errors.New("unauthorized: user does not own this wallet")
)

type Repository interface {
	GetByID(ctx context.Context, walletID string) (*Wallet, error)
	DeleteByIDAndUserID(ctx context.Context, walletID, userID string) error
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// DeleteWallet securely deletes a wallet after verifying ownership.
func (s *Service) DeleteWallet(ctx context.Context, walletID, userID string) error {
	if walletID == "" || userID == "" {
		return fmt.Errorf("invalid arguments: walletID and userID are required")
	}

	// 1. Fetch wallet to verify existence and ownership
	wallet, err := s.repo.GetByID(ctx, walletID)
	if err != nil {
		return fmt.Errorf("failed to retrieve wallet: %w", err)
	}
	if wallet == nil {
		return ErrWalletNotFound
	}

	// 2. Strict ownership verification
	if wallet.UserID != userID {
		return ErrUnauthorized
	}

	// 3. Delete using composite constraint (walletID + userID)
	if err := s.repo.DeleteByIDAndUserID(ctx, walletID, userID); err != nil {
		return fmt.Errorf("failed to delete wallet: %w", err)
	}

	return nil
}

package wallet

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/stellar/go/clients/horizonclient"
)

type Config struct {
	HorizonURL string `mapstructure:"horizon_url" yaml:"horizon_url"`
	UseTestnet bool   `mapstructure:"use_testnet" yaml:"use_testnet"`
}

type WalletService struct {
	horizonClient horizonclient.ClientInterface
}

// NewService initializes the Horizon client dynamically from the configuration.
func NewService(cfg Config) (*WalletService, error) {
	if cfg.HorizonURL == "" {
		return nil, errors.New("invalid configuration: horizon_url must not be empty")
	}

	// Construct dynamic client targeting configured network URL
	client := &horizonclient.Client{
		HorizonURL: cfg.HorizonURL,
		HTTP:       &http.Client{Timeout: 15 * time.Second},
	}

	return &WalletService{
		horizonClient: client,
	}, nil
}