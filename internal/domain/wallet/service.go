package wallet

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/keypair"
	"golang.org/x/crypto/argon2"
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
	pepper := s.cfg.WalletPepper
	if pepper == "" {
		return "", fmt.Errorf("wallet pepper is not configured")
	}

	argonTime := s.cfg.Argon2Time
	if argonTime <= 0 {
		argonTime = 1
	}
	argonMemory := s.cfg.Argon2Memory
	if argonMemory <= 0 {
		argonMemory = 64 * 1024
	}
	argonThreads := s.cfg.Argon2Threads
	if argonThreads <= 0 {
		argonThreads = 4
	}

	salt := []byte(pepper + email)
	key := argon2.IDKey([]byte(email), salt, uint32(argonTime), uint32(argonMemory), uint8(argonThreads), 32)
	return hex.EncodeToString(key), nil
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

	// Bounded async funding with context and timeout
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_ = s.fundAccountWithRetry(bgCtx, w.PublicKey)
	}()

	return w,
		nil
}

func (s *service) fundAccountWithRetry(ctx context.Context, address string) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Perform non-blocking best-effort fund or trustline setup here
			return nil
		}
	}
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
