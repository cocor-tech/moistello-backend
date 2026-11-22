package wallet

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/crypto"
)

func TestWallet_DecryptSecret(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)

	secret := "SBBCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	ciphertext, nonce, err := crypto.Encrypt([]byte(secret), key)
	require.NoError(t, err)

	w := &Wallet{
		ID:                 "w1",
		EncryptedSecretKey: ciphertext,
		EncryptionNonce:    nonce,
	}

	// 1. Success with correct key
	decrypted, err := w.DecryptSecret(key)
	require.NoError(t, err)
	assert.Equal(t, secret, decrypted)

	// 2. Failure with wrong key
	wrongKey := make([]byte, 32)
	_, err = w.DecryptSecret(wrongKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decrypting secret key")

	// 3. Failure when secret key or nonce is empty
	wEmptySecret := &Wallet{EncryptionNonce: nonce}
	_, err = wEmptySecret.DecryptSecret(key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "wallet has no encrypted secret key")

	wEmptyNonce := &Wallet{EncryptedSecretKey: ciphertext}
	_, err = wEmptyNonce.DecryptSecret(key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "wallet has no encrypted secret key")
}
