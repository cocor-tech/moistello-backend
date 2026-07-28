package wallet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"fmt"
)

type WalletType string

const (
	WalletTypeAuto     WalletType = "auto"
	WalletTypeFreighter WalletType = "freighter"
	WalletTypePasskey  WalletType = "passkey"
)

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

// DecryptSecret decrypts the Stellar secret key using the passkey seed
func (w *Wallet) DecryptSecret(passkeySeed []byte) (string, error) {
	key := sha256.Sum256(passkeySeed)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("creating cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("creating GCM: %w", err)
	}

	plaintext, err := aesGCM.Open(nil, w.EncryptionNonce, w.EncryptedSecretKey, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting: %w", err)
	}

	return string(plaintext), nil
}