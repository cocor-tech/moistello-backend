package wallet

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/stellar/go/keypair"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/clients/horizonclient"
)

type Service interface {
	CreateWallet(ctx context.Context, userID string, passkeySeed []byte) (*Wallet, error)
	SignTransaction(ctx context.Context, walletID string, passkeySeed []byte, txnXDR string) (string, error)
	GetWallets(ctx context.Context, userID string) ([]Wallet, error)
	DeleteWallet(ctx context.Context, userID, walletID string) error
}

type Config struct {
	MasterSecretKey  string // master XLM pool secret key
	MasterPublicKey  string
	HorizonURL       string
	USDCIssuer       string // Stellar USDC issuer (mainnet or testnet)
	NetworkPassphrase string
	MinBalanceXLM    float64 // XLM to fund per wallet (~2)
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

func (s *service) CreateWallet(ctx context.Context, userID string, passkeySeed []byte) (*Wallet, error) {
	// 1. Generate Stellar keypair
	kp, err := keypair.Random()
	if err != nil {
		return nil, fmt.Errorf("generating keypair: %w", err)
	}

	// 2. Fund account from master pool
	if err := s.fundAccount(kp.Address()); err != nil {
		return nil, fmt.Errorf("funding account: %w", err)
	}

	// 3. Set USDC trustline
	if err := s.setTrustline(kp); err != nil {
		return nil, fmt.Errorf("setting trustline: %w", err)
	}

	// 4. Encrypt secret key with passkey seed
	encKey, nonce, err := encryptSecret(kp.Seed(), passkeySeed)
	if err != nil {
		return nil, fmt.Errorf("encrypting secret key: %w", err)
	}

	// 5. Store in database
	w := &Wallet{
		UserID:             userID,
		PublicKey:          kp.Address(),
		EncryptedSecretKey: encKey,
		EncryptionNonce:    nonce,
		WalletType:         WalletTypeAuto,
		IsPrimary:          true,
	}
	if err := s.repo.Create(ctx, w); err != nil {
		return nil, fmt.Errorf("creating wallet record: %w", err)
	}

	return w, nil
}

func (s *service) fundAccount(destination string) error {
	// Load master account
	masterAcc, err := s.horizon.AccountDetail(horizonclient.AccountRequest{
		AccountID: s.master.Address(),
	})
	if err != nil {
		return fmt.Errorf("loading master account: %w", err)
	}

	// TODO: calculate proper stroop amount from s.cfg.MinBalanceXLM
	tx, err := txnbuild.NewTransaction(
		txnbuild.TransactionParams{
			SourceAccount:        &masterAcc,
			IncrementSequenceNum: true,
			Operations: []txnbuild.Operation{
				&txnbuild.CreateAccount{
					Destination: destination,
					Amount:      fmt.Sprintf("%.7f", s.cfg.MinBalanceXLM),
				},
			},
			BaseFee: txnbuild.MinBaseFee,
			Preconditions: txnbuild.Preconditions{
				TimeBounds: txnbuild.NewInfiniteTimeout(),
			},
		},
	)
	if err != nil {
		return fmt.Errorf("building fund tx: %w", err)
	}

	tx, err = tx.Sign(s.cfg.NetworkPassphrase, s.master)
	if err != nil {
		return fmt.Errorf("signing fund tx: %w", err)
	}

	txe, err := tx.Base64()
	if err != nil {
		return fmt.Errorf("encoding fund tx: %w", err)
	}

	_, err = s.horizon.SubmitTransactionXDR(txe)
	if err != nil {
		return fmt.Errorf("submitting fund tx: %w", err)
	}

	return nil
}

func (s *service) setTrustline(kp *keypair.Full) error {
	account, err := s.horizon.AccountDetail(horizonclient.AccountRequest{
		AccountID: kp.Address(),
	})
	if err != nil {
		return fmt.Errorf("loading account for trustline: %w", err)
	}

	tx, err := txnbuild.NewTransaction(
		txnbuild.TransactionParams{
			SourceAccount:        &account,
			IncrementSequenceNum: true,
			Operations: []txnbuild.Operation{
				&txnbuild.ChangeTrust{
					Line: txnbuild.ChangeTrustAssetWrapper{
						Asset: txnbuild.CreditAsset{
							Code:   "USDC",
							Issuer: s.cfg.USDCIssuer,
						},
					},
				},
			},
			BaseFee: txnbuild.MinBaseFee,
			Preconditions: txnbuild.Preconditions{
				TimeBounds: txnbuild.NewInfiniteTimeout(),
			},
		},
	)
	if err != nil {
		return fmt.Errorf("building trustline tx: %w", err)
	}

	tx, err = tx.Sign(s.cfg.NetworkPassphrase, kp)
	if err != nil {
		return fmt.Errorf("signing trustline tx: %w", err)
	}

	txe, err := tx.Base64()
	if err != nil {
		return fmt.Errorf("encoding trustline tx: %w", err)
	}

	_, err = s.horizon.SubmitTransactionXDR(txe)
	if err != nil {
		return fmt.Errorf("submitting trustline tx: %w", err)
	}

	return nil
}

func (s *service) SignTransaction(ctx context.Context, walletID string, passkeySeed []byte, txnXDR string) (string, error) {
	wallet, err := s.repo.FindByID(ctx, walletID)
	if err != nil {
		return "", fmt.Errorf("wallet not found: %w", err)
	}
	if len(wallet.EncryptedSecretKey) == 0 || len(wallet.EncryptionNonce) == 0 {
		return "", fmt.Errorf("wallet has no encrypted secret key")
	}

	secretKey, err := decryptSecret(wallet.EncryptedSecretKey, wallet.EncryptionNonce, passkeySeed)
	if err != nil {
		return "", fmt.Errorf("decrypting secret key: %w", err)
	}

	kp, err := keypair.ParseFull(secretKey)
	if err != nil {
		return "", fmt.Errorf("parsing keypair: %w", err)
	}

	genericTx, err := txnbuild.TransactionFromXDR(txnXDR)
	if err != nil {
		return "", fmt.Errorf("parsing transaction XDR: %w", err)
	}

	tx, ok := genericTx.Transaction()
	if !ok {
		return "", fmt.Errorf("unsupported transaction type (expected a regular Transaction, not FeeBump)")
	}

	tx, err = tx.Sign(s.cfg.NetworkPassphrase, kp)
	if err != nil {
		return "", fmt.Errorf("signing transaction: %w", err)
	}

	signedXDR, err := tx.Base64()
	if err != nil {
		return "", fmt.Errorf("encoding signed XDR: %w", err)
	}

	return signedXDR, nil
}

func (s *service) GetWallets(ctx context.Context, userID string) ([]Wallet, error) {
	return s.repo.FindByUserID(ctx, userID)
}

func (s *service) DeleteWallet(ctx context.Context, userID, walletID string) error {
	return s.repo.Delete(ctx, walletID)
}

// encryptSecret encrypts the Stellar secret key using AES-256-GCM
// The encryption key is derived from the passkey seed via SHA-256
func encryptSecret(secretKey string, passkeySeed []byte) (encrypted []byte, nonce []byte, err error) {
	key := sha256.Sum256(passkeySeed)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, nil, fmt.Errorf("creating cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("creating GCM: %w", err)
	}

	nonce = make([]byte, aesGCM.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generating nonce: %w", err)
	}

	ciphertext := aesGCM.Seal(nil, nonce, []byte(secretKey), nil)
	return ciphertext, nonce, nil
}

// decryptSecret decrypts the Stellar secret key using the passkey seed
func decryptSecret(encrypted []byte, nonce []byte, passkeySeed []byte) (string, error) {
	key := sha256.Sum256(passkeySeed)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("creating cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating GCM: %w", err)
	}

	plaintext, err := aesGCM.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting: %w", err)
	}

	return string(plaintext), nil
}

// DeriveEncryptionKey is exposed for the frontend to use
func DeriveEncryptionKey(passkeySeed []byte) string {
	key := sha256.Sum256(passkeySeed)
	return hex.EncodeToString(key[:])
}
