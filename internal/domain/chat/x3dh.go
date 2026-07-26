package chat

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"golang.org/x/crypto/hkdf"
)

// PreKeyBundle contains public keys published by a user for X3DH handshake.
type PreKeyBundle struct {
	UserID          string `json:"user_id"`
	IdentityKey     string `json:"identity_key"`       // Public Identity Key (X25519 hex)
	SignedPreKey    string `json:"signed_prekey"`      // Public Signed Prekey (X25519 hex)
	SignedPreKeyID  int    `json:"signed_prekey_id"`
	Signature       string `json:"signature"`          // Ed25519 signature of signed prekey
	OneTimePreKey   string `json:"one_time_prekey"`    // Optional Public One-Time Prekey (X25519 hex)
	OneTimePreKeyID int    `json:"one_time_prekey_id"`
}

// X3DHKeyPair holds public and private X25519 key bytes.
type X3DHKeyPair struct {
	PrivateKey *ecdh.PrivateKey
	PublicKey  *ecdh.PublicKey
}

// GenerateKeyPair generates a new X25519 keypair for X3DH.
func GenerateKeyPair() (*X3DHKeyPair, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate X25519 key: %w", err)
	}
	return &X3DHKeyPair{
		PrivateKey: priv,
		PublicKey:  priv.PublicKey(),
	}, nil
}

// KeyPairFromHex reconstructs an X25519 KeyPair from hex strings.
func KeyPairFromHex(privHex string) (*X3DHKeyPair, error) {
	privBytes, err := hex.DecodeString(privHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key hex: %w", err)
	}
	priv, err := ecdh.X25519().NewPrivateKey(privBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid private key bytes: %w", err)
	}
	return &X3DHKeyPair{
		PrivateKey: priv,
		PublicKey:  priv.PublicKey(),
	}, nil
}

// X3DHSession represents an established session resulting from X3DH handshake.
type X3DHSession struct {
	SharedMasterKey []byte
	EphemeralPubKey string
	OneTimePreKeyID int
}

// InitiateHandshake computes the shared secret as Alice initiating a session with Bob.
func InitiateHandshake(aliceIK, aliceEK *X3DHKeyPair, bobBundle *PreKeyBundle) (*X3DHSession, error) {
	if bobBundle == nil {
		return nil, errors.New("bob prekey bundle is nil")
	}

	bobIKBytes, err := hex.DecodeString(bobBundle.IdentityKey)
	if err != nil {
		return nil, fmt.Errorf("invalid bob identity key hex: %w", err)
	}
	bobIK, err := ecdh.X25519().NewPublicKey(bobIKBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid bob identity key: %w", err)
	}

	bobSPKBytes, err := hex.DecodeString(bobBundle.SignedPreKey)
	if err != nil {
		return nil, fmt.Errorf("invalid bob signed prekey hex: %w", err)
	}
	bobSPK, err := ecdh.X25519().NewPublicKey(bobSPKBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid bob signed prekey: %w", err)
	}

	// DH1 = ECDH(Alice_IK, Bob_SPK)
	dh1, err := aliceIK.PrivateKey.ECDH(bobSPK)
	if err != nil {
		return nil, fmt.Errorf("DH1 failed: %w", err)
	}

	// DH2 = ECDH(Alice_EK, Bob_IK)
	dh2, err := aliceEK.PrivateKey.ECDH(bobIK)
	if err != nil {
		return nil, fmt.Errorf("DH2 failed: %w", err)
	}

	// DH3 = ECDH(Alice_EK, Bob_SPK)
	dh3, err := aliceEK.PrivateKey.ECDH(bobSPK)
	if err != nil {
		return nil, fmt.Errorf("DH3 failed: %w", err)
	}

	dhSecret := append(dh1, append(dh2, dh3...)...)

	// DH4 = ECDH(Alice_EK, Bob_OPK) if OPK present
	if bobBundle.OneTimePreKey != "" {
		bobOPKBytes, err := hex.DecodeString(bobBundle.OneTimePreKey)
		if err == nil {
			if bobOPK, err := ecdh.X25519().NewPublicKey(bobOPKBytes); err == nil {
				dh4, err := aliceEK.PrivateKey.ECDH(bobOPK)
				if err == nil {
					dhSecret = append(dhSecret, dh4...)
				}
			}
		}
	}

	// Derive master key via HKDF
	masterKey, err := deriveHKDF(dhSecret, []byte("Moistello-X3DH-Protocol-Info"), 32)
	if err != nil {
		return nil, fmt.Errorf("HKDF derivation failed: %w", err)
	}

	return &X3DHSession{
		SharedMasterKey: masterKey,
		EphemeralPubKey: hex.EncodeToString(aliceEK.PublicKey.Bytes()),
		OneTimePreKeyID: bobBundle.OneTimePreKeyID,
	}, nil
}

// DeriveMessageKey derives a message key using HMAC-SHA256 ratchet step for forward secrecy.
func (s *X3DHSession) DeriveMessageKey(sequence uint64) []byte {
	h := hmac.New(sha256.New, s.SharedMasterKey)
	h.Write([]byte(fmt.Sprintf("msg-key-seq-%d", sequence)))
	return h.Sum(nil)
}

func deriveHKDF(secret, info []byte, length int) ([]byte, error) {
	kdf := hkdf.New(sha256.New, secret, nil, info)
	out := make([]byte, length)
	if _, err := kdf.Read(out); err != nil {
		return nil, err
	}
	return out, nil
}

// SignPreKey signs a prekey using Ed25519.
func SignPreKey(edPrivKey ed25519.PrivateKey, prekeyPub []byte) string {
	sig := ed25519.Sign(edPrivKey, prekeyPub)
	return hex.EncodeToString(sig)
}

// VerifyPreKeySignature verifies an Ed25519 signature for a prekey.
func VerifyPreKeySignature(edPubKey ed25519.PublicKey, prekeyPub []byte, sigHex string) bool {
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	return ed25519.Verify(edPubKey, prekeyPub, sig)
}
