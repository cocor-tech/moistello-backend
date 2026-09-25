package crypto

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key, err := ParseEncryptionKey(hex.EncodeToString(bytes.Repeat([]byte{0xAB}, 32)))
	require.NoError(t, err)
	secret := []byte("S3CR3T-KEY-holder")

	ciphertext, nonce, err := Encrypt(secret, key)
	require.NoError(t, err)
	assert.NotEmpty(t, ciphertext)
	assert.NotEqual(t, secret, ciphertext)
	assert.Len(t, nonce, 12) // AES-GCM standard nonce size

	plaintext, err := Decrypt(ciphertext, nonce, key)
	require.NoError(t, err)
	assert.Equal(t, string(secret), string(plaintext))
}

func TestEncryptDecrypt_ShortKeyIsHashed(t *testing.T) {
	// Keys shorter than 32 bytes are SHA-256 hashed before use, so a raw
	// passkey seed round-trips without requiring a pre-derived key.
	seed := []byte("passkey-seed")

	ciphertext, nonce, err := Encrypt([]byte("secret-key"), seed)
	require.NoError(t, err)

	plaintext, err := Decrypt(ciphertext, nonce, seed)
	require.NoError(t, err)
	assert.Equal(t, "secret-key", string(plaintext))
}

func TestDecrypt_LegacyHashed32ByteKey(t *testing.T) {
	// Legacy wallets encrypted by first SHA-256 hashing a 32-byte seed. The
	// 32-byte key itself must still decrypt them via the hashed fallback.
	seed := bytes.Repeat([]byte{0x42}, 32)
	legacyKey := sha256.Sum256(seed)

	ciphertext, nonce, err := Encrypt([]byte("legacy-secret"), legacyKey[:])
	require.NoError(t, err)

	plaintext, err := Decrypt(ciphertext, nonce, seed)
	require.NoError(t, err)
	assert.Equal(t, "legacy-secret", string(plaintext))
}

func TestDecrypt_WrongKeyFails(t *testing.T) {
	keyA, err := ParseEncryptionKey(hex.EncodeToString(bytes.Repeat([]byte{0x01}, 32)))
	require.NoError(t, err)
	keyB, err := ParseEncryptionKey(hex.EncodeToString(bytes.Repeat([]byte{0x02}, 32)))
	require.NoError(t, err)

	ciphertext, nonce, err := Encrypt([]byte("secret"), keyA)
	require.NoError(t, err)

	_, err = Decrypt(ciphertext, nonce, keyB)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decrypting")
}

func TestDecrypt_NoKeyProvided(t *testing.T) {
	ciphertext, nonce, err := Encrypt([]byte("secret"), []byte("key"))
	require.NoError(t, err)

	_, err = Decrypt(ciphertext, nonce)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no decryption key provided")
}

func TestDecrypt_MissingCiphertextOrNonce(t *testing.T) {
	_, err := Decrypt(nil, []byte("nonce"), []byte("key"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ciphertext and nonce are required")

	_, err = Decrypt([]byte("data"), nil, []byte("key"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ciphertext and nonce are required")
}

func TestParseEncryptionKey(t *testing.T) {
	resolved, err := ParseEncryptionKey(hex.EncodeToString(bytes.Repeat([]byte{0xAA}, 32)))
	require.NoError(t, err)
	assert.Len(t, resolved, 32)

	_, err = ParseEncryptionKey("not-hex")
	assert.Error(t, err)

	_, err = ParseEncryptionKey(hex.EncodeToString(bytes.Repeat([]byte{0xAA}, 16)))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must be 32 bytes")
}

func TestDeriveEncryptionKey(t *testing.T) {
	encoded := DeriveEncryptionKey([]byte("passkey-seed"))
	assert.Len(t, encoded, 64)
	raw, err := hex.DecodeString(encoded)
	require.NoError(t, err)
	assert.Len(t, raw, 32)

	// Deterministic.
	assert.Equal(t, encoded, DeriveEncryptionKey([]byte("passkey-seed")))
	// Unique per seed.
	assert.NotEqual(t, encoded, DeriveEncryptionKey([]byte("other-seed")))
}
