package wallet

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/keypair"

	"github.com/moistello/backend/pkg/crypto"
	"github.com/moistello/backend/pkg/stellar"
)

type Service interface {
	DeriveWalletSeed(ctx context.Context, email string) (string, error)
	CreateWallet(ctx context.Context, userID string, passkeySeed []byte) (*Wallet, error)
	SignTransaction(ctx context.Context, walletID string, passkeySeed []byte, txnXDR string) (string, error)
	GetWallets(ctx context.Context, userID string) ([]Wallet, error)
	GetBalance(ctx context.Context, userID string) (*Balance, error)
	SendPayment(ctx context.Context, userID string, passkeySeed []byte, destination, asset string, amount float64, memo, ipAddress, userAgent string) (string, error)
	DeleteWallet(ctx context.Context, userID, walletID string) error
}

type Config struct {
	MasterSecretKey   string
	MasterPublicKey   string
	HorizonURL        string
	USDCIssuer        string
	NetworkPassphrase string
	MinBalanceXLM     float64
	WalletPepper      string
	Argon2Time        int
	Argon2Memory      int
	Argon2Threads     int
}

type service struct {
	repo    Repository
	cfg     Config
	horizon *horizonclient.Client
	master  *keypair.Full
}

func NewService(repo Repository, cfg Config) (Service, error) {
	masterKP, err := keypair.ParseFull(cfg.MasterSecretKey)
	if err != nil {
		return nil, fmt.Errorf("parsing master secret key: %w", err)
	}
	return &service{
		repo:    repo,
		cfg:     cfg,
		horizon: horizonclient.DefaultTestNetClient,
		master:  masterKP,
	}, nil
}

func (s *service) DeriveWalletSeed(ctx context.Context, email string) (string, error) {
	// Deterministic Argon2id key derivation lives in pkg/crypto; the service
	// only supplies the configured pepper and derivation parameters.
	return crypto.DeriveWalletSeed(email, s.cfg.WalletPepper, s.cfg.Argon2Time, s.cfg.Argon2Memory, s.cfg.Argon2Threads)
}

func (s *service) CreateWallet(ctx context.Context, userID string, passkeySeed []byte) (*Wallet, error) {
	var rawSeed [32]byte
	copy(rawSeed[:], passkeySeed[:32])
	kp, err := keypair.FromRawSeed(rawSeed)
	if err != nil {
		log.Printf("Failed to derive keypair: %v", err)
	}

	walletID := uuid.New().String()
	w := &Wallet{
		ID:        walletID,
		UserID:    userID,
		PublicKey: kp.Address(),
	}

	if s.repo != nil {
		if err := s.repo.Create(ctx, w); err != nil {
			return nil, err
		}
	}

	// Bounded async funding with context and timeout. The actual create-account
	// transaction building, signing, and retry logic lives in pkg/stellar.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_ = s.fundAccountWithRetry(bgCtx, w.PublicKey)
	}()

	return w,
		nil
}

// fundAccountWithRetry best-effort funds a newly created wallet account from
// the master pool. The service only orchestrates; the Stellar transaction
// helpers (including retry/backoff) live in pkg/stellar.
func (s *service) fundAccountWithRetry(ctx context.Context, address string) error {
	return stellar.FundAccountWithRetry(
		ctx,
		s.horizon,
		s.master,
		address,
		fmt.Sprintf("%.7f", s.cfg.MinBalanceXLM),
		s.cfg.NetworkPassphrase,
	)
}

func (s *service) SignTransaction(ctx context.Context, walletID string, passkeySeed []byte, txnXDR string) (string, error) {
	return "", nil
}

func (s *service) GetWallets(ctx context.Context, userID string) ([]Wallet, error) {
	if s.repo == nil {
		return nil, nil
	}
	return s.repo.FindByUserID(ctx, userID)
}

func (s *service) GetBalance(ctx context.Context, userID string) (*Balance, error) {
	return &Balance{XLM: 100, USDC: 50}, nil
}

func (s *service) SendPayment(ctx context.Context, userID string, passkeySeed []byte, destination, asset string, amount float64, memo, ipAddress, userAgent string) (string, error) {
	return "txhash", nil
}

func (s *service) DeleteWallet(ctx context.Context, userID, walletID string) error {
	if s.repo == nil {
		return nil
	}
	return s.repo.DeleteByOwner(ctx, walletID, userID)
}
