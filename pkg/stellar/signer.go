package stellar

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stellar/go/keypair"
	"github.com/stellar/go/strkey"
)

type Signer struct {
	publicKey ed25519.PublicKey
	secretKey ed25519.PrivateKey
	address   string
}

// NewSigner creates a signer from a Stellar secret key (S-prefixed strkey).
func NewSigner(secretKey string) (*Signer, error) {
	kp, err := keypair.ParseFull(secretKey)
	if err != nil {
		return nil, fmt.Errorf("parsing secret key: %w", err)
	}
	seedBytes, err := strkey.Decode(strkey.VersionByteSeed, secretKey)
	if err != nil {
		return nil, fmt.Errorf("decoding secret key: %w", err)
	}
	privKey := ed25519.NewKeyFromSeed(seedBytes)
	return &Signer{
		publicKey: privKey.Public().(ed25519.PublicKey),
		secretKey: privKey,
		address:   kp.Address(),
	}, nil
}

func (s *Signer) Address() string { return s.address }

// NewSignerFromHex creates a signer from hex-encoded keys.
// publicKeyHex: 64-char hex Ed25519 public key
// secretKeyHex: 64-char hex Ed25519 secret key (seed)
func NewSignerFromHex(publicKeyHex, secretKeyHex string) (*Signer, error) {
	pubBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decoding public key: %w", err)
	}
	secBytes, err := hex.DecodeString(secretKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decoding secret key: %w", err)
	}

	if len(pubBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key must be %d bytes", ed25519.PublicKeySize)
	}
	if len(secBytes) != ed25519.SeedSize {
		return nil, fmt.Errorf("secret key seed must be %d bytes", ed25519.SeedSize)
	}

	privKey := ed25519.NewKeyFromSeed(secBytes)
	return &Signer{publicKey: pubBytes, secretKey: privKey}, nil
}

// NewSignerFromFile creates a signer from PEM or raw key files.
func NewSignerFromFile(pubPath, secPath string) (*Signer, error) {
	pubPath, err := validatedKeyPath(pubPath)
	if err != nil {
		return nil, fmt.Errorf("validating public key path: %w", err)
	}
	secPath, err = validatedKeyPath(secPath)
	if err != nil {
		return nil, fmt.Errorf("validating secret key path: %w", err)
	}
	// Paths are resolved and checked as regular files above; this is the only
	// remaining dynamic read and is intentionally limited to caller-selected
	// key files.
	pubData, err := os.ReadFile(pubPath) // #nosec G304 -- validated regular key path
	if err != nil {
		return nil, fmt.Errorf("reading public key file: %w", err)
	}
	secData, err := os.ReadFile(secPath) // #nosec G304 -- validated regular key path
	if err != nil {
		return nil, fmt.Errorf("reading secret key file: %w", err)
	}

	pubHex := strings.TrimSpace(string(pubData))
	secHex := strings.TrimSpace(string(secData))
	return NewSignerFromHex(pubHex, secHex)
}

func validatedKeyPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("path is not a regular file")
	}
	return abs, nil
}

func (s *Signer) Sign(message []byte) ([]byte, error) {
	sig := ed25519.Sign(s.SecretKey(), message)
	return sig, nil
}

func (s *Signer) PublicKeyBytes() []byte        { return s.publicKey }
func (s *Signer) SecretKey() ed25519.PrivateKey { return s.secretKey }
func (s *Signer) Verify(message, signature []byte) bool {
	return ed25519.Verify(s.publicKey, message, signature)
}
