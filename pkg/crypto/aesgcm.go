// Package crypto provides the low-level cryptographic primitives used across
// the application: AES-256-GCM encryption/decryption of wallet secret keys and
// deterministic Argon2id key derivation. Keeping crypto here (instead of the
// domain service layer) lets services focus on orchestration and security
// policy while the primitives stay in a single, auditable package.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// encryptionKeyLen is the required length of an AES-256 key in bytes.
const encryptionKeyLen = 32

// ParseEncryptionKey decodes a hex-encoded encryption key into raw bytes.
// The key must be exactly 32 bytes (64 hex characters).
func ParseEncryptionKey(hexKey string) ([]byte, error) {
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex encoding: %w", err)
	}
	if len(raw) != encryptionKeyLen {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(raw))
	}
	return raw, nil
}

// Encrypt encrypts plaintext with AES-256-GCM using the provided encryption
// key. A fresh random nonce is generated for every call. On success it returns
// the ciphertext and the nonce that must be persisted alongside it.
func Encrypt(plaintext, key []byte) (ciphertext, nonce []byte, err error) {
	aesKey := encryptionKey(key)

	block, err := aes.NewCipher(aesKey)
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

	ciphertext = aesGCM.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Decrypt decrypts an AES-256-GCM ciphertext, trying each candidate key in
// order and returning the plaintext of the first successful attempt. Non-32
// byte keys are hashed with SHA-256. For exactly 32-byte keys it also attempts
// the SHA-256 hash of the key to support legacy wallets that derived the AES
// key by hashing a 32-byte seed.
func Decrypt(ciphertext, nonce []byte, keys ...[]byte) ([]byte, error) {
	if len(ciphertext) == 0 || len(nonce) == 0 {
		return nil, fmt.Errorf("ciphertext and nonce are required")
	}

	var lastErr error
	for _, rawKey := range keys {
		if len(rawKey) == 0 {
			continue
		}

		aesKey := encryptionKey(rawKey)

		if plaintext, err := decryptWithKey(ciphertext, nonce, aesKey); err == nil {
			return plaintext, nil
		} else {
			lastErr = err
		}

		// Legacy wallets hashed a 32-byte seed before using it as the AES key.
		if len(rawKey) == encryptionKeyLen {
			hashed := sha256.Sum256(rawKey)
			if plaintext, err := decryptWithKey(ciphertext, nonce, hashed[:]); err == nil {
				return plaintext, nil
			} else {
				lastErr = err
			}
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("decrypting: %w", lastErr)
	}
	return nil, fmt.Errorf("no decryption key provided")
}

// decryptWithKey opens an AES-256-GCM ciphertext with a concrete 32-byte key.
func decryptWithKey(ciphertext, nonce, aesKey []byte) ([]byte, error) {
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

// encryptionKey normalizes an arbitrary key to the 32 bytes required by
// AES-256. 32-byte keys are used verbatim; everything else is SHA-256 hashed.
func encryptionKey(rawKey []byte) []byte {
	if len(rawKey) == encryptionKeyLen {
		return rawKey
	}
	hashed := sha256.Sum256(rawKey)
	return hashed[:]
}

// DeriveEncryptionKey derives a hex-encoded SHA-256 key from a raw seed (for
// example a passkey seed) for use with AES-256-GCM.
func DeriveEncryptionKey(seed []byte) string {
	sum := sha256.Sum256(seed)
	return hex.EncodeToString(sum[:])
}
