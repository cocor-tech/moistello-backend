package wallet

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/stellar/go/keypair"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/clients/horizonclient"
)

type Service interface {
	CreateWallet(ctx context.Context, userID string, passkeySeed []byte) (*Wallet, error)
	SignTransaction(ctx context.Context, walletID string, passkeySeed []byte, txnXDR string) (string, error)
	GetWallets(ctx context.Context, userID string) ([]Wallet, error)
	GetBalance(ctx context.Context, userID string) (*Balance, error)
	SendPayment(ctx context.Context, userID string, passkeySeed []byte, destination, asset string, amount float64, memo, ipAddress, userAgent string) (string, error)
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
	// 1. Generate Stellar keypair from the deterministic seed
	// The seed is derived from email + server pepper (see deriveWalletSeed in auth handler).
	// This ensures the same email always produces the same wallet address.
	var rawSeed [32]byte
	copy(rawSeed[:], passkeySeed[:32])
	kp, err := keypair.FromRawSeed(rawSeed)
	if err != nil {
		log.Printf("[wallet] ERROR deriving keypair from seed: %v", err)
		return nil, fmt.Errorf("deriving keypair from seed: %w", err)
	}

	// 2. Encrypt secret key with passkey seed
	encKey, nonce, err := encryptSecret(kp.Seed(), passkeySeed)
	if err != nil {
		log.Printf("[wallet] ERROR encrypting key: %v", err)
		return nil, fmt.Errorf("encrypting secret key: %w", err)
	}

	// 3. Store in database FIRST (before slow Horizon ops).
	// Use the request context so the DB write is cancelled if the caller disconnects,
	// preventing orphaned operations under high load (#51).
	w := &Wallet{
		UserID:             userID,
		PublicKey:          kp.Address(),
		EncryptedSecretKey: encKey,
		EncryptionNonce:    nonce,
		WalletType:         WalletTypeAuto,
		IsPrimary:          true,
	}
	if err := s.repo.Create(ctx, w); err != nil {
		log.Printf("[wallet] ERROR storing wallet record for %s: %v", userID, err)
		return nil, fmt.Errorf("creating wallet record: %w", err)
	}
	log.Printf("[wallet] created wallet record %s for user %s", kp.Address(), userID)

	// 4. Fund account from master pool (best-effort)
	if err := s.fundAccount(kp.Address()); err != nil {
		log.Printf("[wallet] ERROR funding account %s: %v", kp.Address(), err)
	} else {
		log.Printf("[wallet] funded account %s with %.1f XLM", kp.Address(), s.cfg.MinBalanceXLM)
	}

	// 5. Set USDC trustline (best-effort)
	if err := s.setTrustline(kp); err != nil {
		log.Printf("[wallet] WARNING trustline failed for %s: %v", kp.Address(), err)
	}

	// 6. Send 1 test USDC from master (issuer) to new wallet (best-effort)
	if err := s.sendTestUSDC(kp.Address()); err != nil {
		log.Printf("[wallet] WARNING sending test USDC to %s: %v", kp.Address(), err)
	}

	return w, nil
}

func (s *service) fundAccount(destination string) error {
	// Load master account with retry (Horizon testnet can transiently 404)
	var lastErr error
	for i := 0; i < 3; i++ {
		masterAcc, err := s.horizon.AccountDetail(horizonclient.AccountRequest{
			AccountID: s.master.Address(),
		})
		if err != nil {
			lastErr = err
			log.Printf("[wallet] fundAccount attempt %d/3 failed: %v", i+1, err)
			time.Sleep(time.Duration(i+1) * time.Second)
			continue
		}

		// Build and submit CreateAccount transaction
		tx, buildErr := txnbuild.NewTransaction(
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
		if buildErr != nil {
			return fmt.Errorf("building fund tx: %w", buildErr)
		}

		tx, signErr := tx.Sign(s.cfg.NetworkPassphrase, s.master)
		if signErr != nil {
			return fmt.Errorf("signing fund tx: %w", signErr)
		}

		txe, encErr := tx.Base64()
		if encErr != nil {
			return fmt.Errorf("encoding fund tx: %w", encErr)
		}

		_, subErr := s.horizon.SubmitTransactionXDR(txe)
		if subErr != nil {
			if hErr, ok := subErr.(*horizonclient.Error); ok {
				log.Printf("[wallet] Horizon error detail: %s | result_xdr: %s", hErr.Problem.Detail, hErr.Problem.Extras["result_xdr"])
			}
			// If tx fails, retry from account load
			lastErr = subErr
			log.Printf("[wallet] fundAccount submit attempt %d/3 failed: %v", i+1, subErr)
			time.Sleep(time.Duration(i+1) * time.Second)
			continue
		}

		return nil
	}
	return fmt.Errorf("fundAccount failed after 3 retries: %w", lastErr)
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

func (s *service) sendTestUSDC(destination string) error {
	masterAcc, err := s.horizon.AccountDetail(horizonclient.AccountRequest{
		AccountID: s.master.Address(),
	})
	if err != nil {
		return fmt.Errorf("loading master account: %w", err)
	}

	tx, err := txnbuild.NewTransaction(
		txnbuild.TransactionParams{
			SourceAccount:        &masterAcc,
			IncrementSequenceNum: true,
			Operations: []txnbuild.Operation{
				&txnbuild.Payment{
					Destination: destination,
					Amount:      "1.0000000",
					Asset: txnbuild.CreditAsset{
						Code:   "USDC",
						Issuer: s.cfg.USDCIssuer,
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
		return fmt.Errorf("building USDC payment tx: %w", err)
	}

	tx, err = tx.Sign(s.cfg.NetworkPassphrase, s.master)
	if err != nil {
		return fmt.Errorf("signing USDC payment tx: %w", err)
	}

	txe, err := tx.Base64()
	if err != nil {
		return fmt.Errorf("encoding USDC payment tx: %w", err)
	}

	_, err = s.horizon.SubmitTransactionXDR(txe)
	if err != nil {
		return fmt.Errorf("submitting USDC payment tx: %w", err)
	}

	log.Printf("[wallet] sent 1.0 USDC to %s", destination)
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

type Balance struct {
	XLM  string `json:"xlm"`
	USDC string `json:"usdc"`
}

func (s *service) GetBalance(ctx context.Context, userID string) (*Balance, error) {
	wallets, err := s.repo.FindByUserID(ctx, userID)
	if err != nil || len(wallets) == 0 {
		return nil, fmt.Errorf("no wallet found")
	}
	pk := wallets[0].PublicKey

	account, err := s.horizon.AccountDetail(horizonclient.AccountRequest{AccountID: pk})
	if err != nil {
		return nil, fmt.Errorf("fetching account from horizon: %w", err)
	}

	bal := &Balance{XLM: "0.0000", USDC: "0.0000"}
	for _, b := range account.Balances {
		if b.Asset.Type == "native" {
			bal.XLM = b.Balance
		} else if b.Asset.Code == "USDC" && b.Asset.Issuer == s.cfg.USDCIssuer {
			bal.USDC = b.Balance
		}
	}
	return bal, nil
}

func (s *service) SendPayment(ctx context.Context, userID string, passkeySeed []byte, destination, asset string, amount float64, memo, ipAddress, userAgent string) (string, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("invalid user ID: %w", err)
	}

	// ── Security Check 1: Self-send ──
	wallets, err := s.repo.FindByUserID(ctx, userID)
	if err != nil || len(wallets) == 0 {
		return "", fmt.Errorf("no wallet found")
	}
	w := &wallets[0]
	if w.PublicKey == destination {
		auditErr := s.repo.RecordWithdrawalAudit(ctx, &WithdrawalRecord{
			ID: uuid.New(), UserID: uid, Destination: destination, Asset: asset, Amount: amount,
			Status: "blocked_self_send", IPAddress: ipAddress, UserAgent: userAgent, CreatedAt: time.Now().UTC(),
		})
		if auditErr != nil { _ = auditErr }
		return "", fmt.Errorf("cannot send to your own wallet address")
	}

	// ── Security Check 2: Rate limit ──
	allowed, err := s.repo.CheckRateLimit(ctx, uid)
	if err != nil || !allowed {
		auditErr := s.repo.RecordWithdrawalAudit(ctx, &WithdrawalRecord{
			ID: uuid.New(), UserID: uid, Destination: destination, Asset: asset, Amount: amount,
			Status: "blocked_rate_limit", IPAddress: ipAddress, UserAgent: userAgent, CreatedAt: time.Now().UTC(),
		})
		if auditErr != nil { _ = auditErr }
		return "", fmt.Errorf("rate limit exceeded: max 3 withdrawals per hour")
	}

	// ── Security Check 3: Daily spending limit ──
	spentToday, err := s.repo.GetDailySpending(ctx, uid)
	if err != nil {
		spentToday = 0
	}
	const dailyLimit = 1000.0
	if spentToday+amount > dailyLimit {
		auditErr := s.repo.RecordWithdrawalAudit(ctx, &WithdrawalRecord{
			ID: uuid.New(), UserID: uid, Destination: destination, Asset: asset, Amount: amount,
			Status: "blocked_daily_limit", IPAddress: ipAddress, UserAgent: userAgent, CreatedAt: time.Now().UTC(),
		})
		if auditErr != nil { _ = auditErr }
		return "", fmt.Errorf("daily spending limit exceeded")
	}

	// ── Security Check 4: Stellar address validation ──
	if _, err := keypair.ParseAddress(destination); err != nil {
		return "", fmt.Errorf("invalid Stellar address: %w", err)
	}

	// ── Record pending audit ──
	auditID := uuid.New()
	_ = s.repo.RecordWithdrawalAudit(ctx, &WithdrawalRecord{
		ID: auditID, UserID: uid, Destination: destination, Asset: asset, Amount: amount,
		Status: "pending", IPAddress: ipAddress, UserAgent: userAgent, CreatedAt: time.Now().UTC(),
	})

	// ── Decrypt key and build transaction ──
	secretKey, err := decryptSecret(w.EncryptedSecretKey, w.EncryptionNonce, passkeySeed)
	if err != nil {
		return "", fmt.Errorf("decrypting secret key: %w", err)
	}

	kp, err := keypair.ParseFull(secretKey)
	if err != nil {
		return "", fmt.Errorf("parsing keypair: %w", err)
	}

	account, err := s.horizon.AccountDetail(horizonclient.AccountRequest{AccountID: kp.Address()})
	if err != nil {
		return "", fmt.Errorf("loading account: %w", err)
	}

	var op txnbuild.Operation
	if asset == "XLM" {
		op = &txnbuild.Payment{
			Destination: destination,
			Amount:      fmt.Sprintf("%.7f", amount),
			Asset:       txnbuild.NativeAsset{},
		}
	} else {
		op = &txnbuild.Payment{
			Destination: destination,
			Amount:      fmt.Sprintf("%.7f", amount),
			Asset:       txnbuild.CreditAsset{Code: "USDC", Issuer: s.cfg.USDCIssuer},
		}
	}

	params := txnbuild.TransactionParams{
		SourceAccount:        &account,
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{op},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
	}
	if memo != "" {
		params.Memo = txnbuild.MemoText(memo)
	}

	tx, err := txnbuild.NewTransaction(params)
	if err != nil {
		return "", fmt.Errorf("building tx: %w", err)
	}

	tx, err = tx.Sign(s.cfg.NetworkPassphrase, kp)
	if err != nil {
		return "", fmt.Errorf("signing tx: %w", err)
	}

	txe, err := tx.Base64()
	if err != nil {
		return "", fmt.Errorf("encoding tx: %w", err)
	}

	resp, err := s.horizon.SubmitTransactionXDR(txe)
	if err != nil {
		_ = s.repo.RecordWithdrawalAudit(ctx, &WithdrawalRecord{
			ID: auditID, UserID: uid, Destination: destination, Asset: asset, Amount: amount,
			Status: "failed_horizon", Failure: err.Error(), IPAddress: ipAddress, UserAgent: userAgent, CreatedAt: time.Now().UTC(),
		})
		return "", fmt.Errorf("submitting tx: %w", err)
	}

	_ = s.repo.RecordWithdrawalAudit(ctx, &WithdrawalRecord{
		ID: auditID, UserID: uid, Destination: destination, Asset: asset, Amount: amount,
		Status: "completed", TxHash: resp.Hash, IPAddress: ipAddress, UserAgent: userAgent, CreatedAt: time.Now().UTC(),
	})
	_ = s.repo.IncrementRateLimit(ctx, uid)

	return resp.Hash, nil
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
