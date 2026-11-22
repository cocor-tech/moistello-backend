package stellar

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stellar/go/keypair"
	"github.com/stellar/go/strkey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestSigner generates a fresh Stellar keypair and builds a Signer from
// the secret key string (S-prefixed strkey). Used by multiple subtests.
func newTestSigner(t *testing.T) (*Signer, *keypair.Full) {
	t.Helper()
	kp, err := keypair.Random()
	require.NoError(t, err)
	s, err := NewSigner(kp.Seed())
	require.NoError(t, err)
	return s, kp
}

// ---------------------------------------------------------------------------
// NewSigner
// ---------------------------------------------------------------------------

func TestNewSigner_ValidSecretKey(t *testing.T) {
	kp, err := keypair.Random()
	require.NoError(t, err)

	s, err := NewSigner(kp.Seed())
	require.NoError(t, err)
	assert.Equal(t, kp.Address(), s.Address())
}

func TestNewSigner_InvalidSecretKey(t *testing.T) {
	_, err := NewSigner("not-a-stellar-key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing secret key")
}

func TestNewSigner_EmptySecretKey(t *testing.T) {
	_, err := NewSigner("")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// NewSignerFromHex
// ---------------------------------------------------------------------------

func TestNewSignerFromHex_ValidKeys(t *testing.T) {
	kp, err := keypair.Random()
	require.NoError(t, err)

	seedBytes, err := strkey.Decode(strkey.VersionByteSeed, kp.Seed())
	require.NoError(t, err)
	privKey := ed25519.NewKeyFromSeed(seedBytes)
	pubBytes := privKey.Public().(ed25519.PublicKey)

	pubHex := hex.EncodeToString(pubBytes)
	secHex := hex.EncodeToString(seedBytes)

	s, err := NewSignerFromHex(pubHex, secHex)
	require.NoError(t, err)
	assert.Equal(t, pubBytes, s.PublicKeyBytes())
}

func TestNewSignerFromHex_InvalidPublicKeyHex(t *testing.T) {
	_, err := NewSignerFromHex("not-hex", "deadbeef")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding public key")
}

func TestNewSignerFromHex_InvalidSecretKeyHex(t *testing.T) {
	validPubHex := hex.EncodeToString(make([]byte, ed25519.PublicKeySize))
	_, err := NewSignerFromHex(validPubHex, "not-hex")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding secret key")
}

func TestNewSignerFromHex_WrongPublicKeyLength(t *testing.T) {
	_, err := NewSignerFromHex(
		hex.EncodeToString([]byte("short")),
		hex.EncodeToString(make([]byte, ed25519.SeedSize)),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "public key must be")
}

func TestNewSignerFromHex_WrongSecretKeyLength(t *testing.T) {
	_, err := NewSignerFromHex(
		hex.EncodeToString(make([]byte, ed25519.PublicKeySize)),
		hex.EncodeToString([]byte("short-seed")),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret key seed must be")
}

// ---------------------------------------------------------------------------
// NewSignerFromFile
// ---------------------------------------------------------------------------

func writeHexFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestNewSignerFromFile_ValidFiles(t *testing.T) {
	kp, err := keypair.Random()
	require.NoError(t, err)

	seedBytes, err := strkey.Decode(strkey.VersionByteSeed, kp.Seed())
	require.NoError(t, err)
	privKey := ed25519.NewKeyFromSeed(seedBytes)
	pubBytes := privKey.Public().(ed25519.PublicKey)

	dir := t.TempDir()
	pubPath := writeHexFile(t, dir, "pub.key", hex.EncodeToString(pubBytes))
	secPath := writeHexFile(t, dir, "sec.key", hex.EncodeToString(seedBytes))

	s, err := NewSignerFromFile(pubPath, secPath)
	require.NoError(t, err)
	assert.Equal(t, pubBytes, s.PublicKeyBytes())
}

func TestNewSignerFromFile_MissingPublicKeyFile(t *testing.T) {
	dir := t.TempDir()
	_, err := NewSignerFromFile(filepath.Join(dir, "nonexistent.pub"), filepath.Join(dir, "nonexistent.sec"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validating public key path")
}

func TestNewSignerFromFile_EmptyPubPath(t *testing.T) {
	_, err := NewSignerFromFile("", "some/path")
	require.Error(t, err)
}

func TestNewSignerFromFile_EmptySecPath(t *testing.T) {
	kp, err := keypair.Random()
	require.NoError(t, err)
	seedBytes, err := strkey.Decode(strkey.VersionByteSeed, kp.Seed())
	require.NoError(t, err)
	privKey := ed25519.NewKeyFromSeed(seedBytes)
	pubBytes := privKey.Public().(ed25519.PublicKey)

	dir := t.TempDir()
	pubPath := writeHexFile(t, dir, "pub.key", hex.EncodeToString(pubBytes))
	_, err = NewSignerFromFile(pubPath, "")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Sign and Verify
// ---------------------------------------------------------------------------

func TestSigner_SignAndVerify_RoundTrip(t *testing.T) {
	s, _ := newTestSigner(t)
	msg := []byte("authenticate this message")

	sig, err := s.Sign(msg)
	require.NoError(t, err)
	require.NotEmpty(t, sig)

	assert.True(t, s.Verify(msg, sig), "Verify must accept a signature produced by Sign")
}

func TestSigner_Verify_RejectsAlteredMessage(t *testing.T) {
	s, _ := newTestSigner(t)
	msg := []byte("original message")
	sig, err := s.Sign(msg)
	require.NoError(t, err)

	assert.False(t, s.Verify([]byte("tampered message"), sig))
}

func TestSigner_Verify_RejectsAlteredSignature(t *testing.T) {
	s, _ := newTestSigner(t)
	msg := []byte("message")
	sig, err := s.Sign(msg)
	require.NoError(t, err)

	// Flip one byte in the signature.
	sig[0] ^= 0xFF
	assert.False(t, s.Verify(msg, sig))
}

func TestSigner_PublicKeyBytes_Length(t *testing.T) {
	s, _ := newTestSigner(t)
	assert.Len(t, s.PublicKeyBytes(), ed25519.PublicKeySize)
}

func TestSigner_SecretKey_Length(t *testing.T) {
	s, _ := newTestSigner(t)
	assert.Len(t, s.SecretKey(), ed25519.PrivateKeySize)
}
