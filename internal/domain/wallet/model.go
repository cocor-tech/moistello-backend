package wallet

import (
	"fmt"

	"github.com/moistello/backend/pkg/crypto"
)

type WalletType string

const (
	WalletTypeAuto      WalletType = "auto"
	WalletTypeFreighter WalletType = "freighter"
	WalletTypePasskey   WalletType = "passkey"
)

// Balance represents the available XLM and USDC balances for a wallet.
type Balance struct {
	XLM  float64 `json:"xlm"`
	USDC float64 `json:"usdc"`
}

type Wallet struct {
	ID                 string     `json:"id" db:"id"`
	UserID             string     `json:"userId" db:"user_id"`
	PublicKey          string     `json:"publicKey" db:"public_key"`
	EncryptedSecretKey []byte     `json:"-" db:"encrypted_secret_key"`
	EncryptionNonce    []byte     `json:"-" db:"encryption_nonce"`
	WalletType         WalletType `json:"walletType" db:"wallet_type"`
	IsPrimary          bool       `json:"isPrimary" db:"is_primary"`
	CreatedAt          string     `json:"createdAt" db:"created_at"`
	UpdatedAt          string     `json:"updatedAt" db:"updated_at"`
}

// DecryptSecret decrypts the wallet's AES-256-GCM encrypted secret key using
// the provided encryption keys (primary configured key, rotated keys, or a
// legacy passkey seed). It delegates to pkg/crypto so the wallet domain stays
// free of cryptographic primitives and returns the first successful decryption.
func (w *Wallet) DecryptSecret(keys ...[]byte) (string, error) {
	if len(w.EncryptedSecretKey) == 0 || len(w.EncryptionNonce) == 0 {
		return "", fmt.Errorf("wallet has no encrypted secret key")
	}
	plaintext, err := crypto.Decrypt(w.EncryptedSecretKey, w.EncryptionNonce, keys...)
	if err != nil {
		return "", fmt.Errorf("decrypting secret key: %w", err)
	}
	return string(plaintext), nil
}
