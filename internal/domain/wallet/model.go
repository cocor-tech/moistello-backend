package wallet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

// ParseEncryptionKey decodes a hex-encoded encryption key into raw bytes.
// The key must be exactly 32 bytes (64 hex characters).
func ParseEncryptionKey(hexKey string) ([]byte, error) {
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex encoding: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(raw))
	}
	return raw, nil
}

// DecryptSecret decrypts the Stellar secret key using the provided encryption keys.
// It iterates through the provided keys (primary configured key, rotated keys, or legacy passkey seed)
// and returns the decrypted secret key upon the first successful decryption.
func (w *Wallet) DecryptSecret(keys ...[]byte) (string, error) {
	if len(w.EncryptedSecretKey) == 0 || len(w.EncryptionNonce) == 0 {
		return "", fmt.Errorf("wallet has no encrypted secret key")
	}

	var lastErr error
	for _, rawKey := range keys {
		if len(rawKey) == 0 {
			continue
		}

		// Try direct key if 32 bytes, or SHA-256 derived key
		var aesKey [32]byte
		if len(rawKey) == 32 {
			copy(aesKey[:], rawKey)
		} else {
			aesKey = sha256.Sum256(rawKey)
		}

		block, err := aes.NewCipher(aesKey[:])
		if err == nil {
			aesGCM, err := cipher.NewGCM(block)
			if err == nil {
				plaintext, err := aesGCM.Open(nil, w.EncryptionNonce, w.EncryptedSecretKey, nil)
				if err == nil {
					return string(plaintext), nil
				}
				lastErr = err
			} else {
				lastErr = err
			}
		} else {
			lastErr = err
		}

		// For 32-byte keys, also attempt SHA-256(rawKey) to support legacy wallets where a 32-byte seed was hashed
		if len(rawKey) == 32 {
			hashedKey := sha256.Sum256(rawKey)
			blockHashed, err := aes.NewCipher(hashedKey[:])
			if err == nil {
				aesGCM, err := cipher.NewGCM(blockHashed)
				if err == nil {
					plaintext, err := aesGCM.Open(nil, w.EncryptionNonce, w.EncryptedSecretKey, nil)
					if err == nil {
						return string(plaintext), nil
					}
					lastErr = err
				}
			}
		}
	}

	if lastErr != nil {
		return "", fmt.Errorf("decrypting secret key: %w", lastErr)
	}
	return "", fmt.Errorf("no decryption key provided")
}
