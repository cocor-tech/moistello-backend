package crypto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveWalletSeed_MissingPepper(t *testing.T) {
	seed, err := DeriveWalletSeed("user@example.com", "", 1, 64*1024, 4)
	assert.Error(t, err)
	assert.Empty(t, seed)
	assert.Contains(t, err.Error(), "wallet pepper is not configured")
}

func TestDeriveWalletSeed_DeterministicAndUniquePerEmail(t *testing.T) {
	seed, err := DeriveWalletSeed("user@example.com", "test-secret-pepper-123", 1, 64*1024, 4)
	require.NoError(t, err)
	assert.Len(t, seed, 64) // 32-byte key, hex-encoded

	// Deterministic: the same email always derives the same seed.
	seed2, err := DeriveWalletSeed("user@example.com", "test-secret-pepper-123", 1, 64*1024, 4)
	require.NoError(t, err)
	assert.Equal(t, seed, seed2)

	// Unique per email: the email participates in the salt.
	seedOther, err := DeriveWalletSeed("other@example.com", "test-secret-pepper-123", 1, 64*1024, 4)
	require.NoError(t, err)
	assert.NotEqual(t, seed, seedOther)
}

func TestDeriveWalletSeed_DifferentPepperChangesSeed(t *testing.T) {
	seedA, err := DeriveWalletSeed("user@example.com", "pepper-a", 1, 64*1024, 4)
	require.NoError(t, err)
	seedB, err := DeriveWalletSeed("user@example.com", "pepper-b", 1, 64*1024, 4)
	require.NoError(t, err)
	assert.NotEqual(t, seedA, seedB)
}

func TestDeriveWalletSeed_DefaultsWhenParamsZero(t *testing.T) {
	// Zero argon2 params fall back to the conservative defaults rather than
	// failing or producing degenerate output.
	seed, err := DeriveWalletSeed("user@example.com", "pepper", 0, 0, 0)
	require.NoError(t, err)
	assert.Len(t, seed, 64)
}
