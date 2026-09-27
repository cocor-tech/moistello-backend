package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	domainJWT "github.com/moistello/backend/internal/domain/auth/jwt"
)

const minRSABits = 2048

// KeyPair represents a parsed JWT signing and verifying key with an associated key ID (kid).
type KeyPair = domainJWT.Key

// ParseKeyPair parses PEM-encoded private and public keys along with an optional key ID (kid).
// If kid is empty, a deterministic key ID based on SHA-256 fingerprint of the public key is generated.
func ParseKeyPair(privPEMBytes, pubPEMBytes []byte, kid string) (*domainJWT.Key, error) {
	privPEMBytes = []byte(strings.TrimSpace(string(privPEMBytes)))
	pubPEMBytes = []byte(strings.TrimSpace(string(pubPEMBytes)))

	if len(pubPEMBytes) == 0 {
		return nil, fmt.Errorf("public key PEM bytes cannot be empty")
	}

	var signingKey any
	var signingMethod jwt.SigningMethod
	var err error
	if len(privPEMBytes) > 0 {
		signingKey, signingMethod, err = ParsePrivateSigningKey(privPEMBytes)
		if err != nil {
			return nil, fmt.Errorf("parsing private signing key: %w", err)
		}
	}

	verifyingKey, verifyingMethod, err := ParsePublicVerifyingKey(pubPEMBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing public verifying key: %w", err)
	}

	if signingMethod != nil && signingMethod.Alg() != verifyingMethod.Alg() {
		return nil, fmt.Errorf("JWT key pair algorithm mismatch: private=%s public=%s", signingMethod.Alg(), verifyingMethod.Alg())
	}

	if kid == "" {
		hash := sha256.Sum256(pubPEMBytes)
		kid = hex.EncodeToString(hash[:8])
	}

	return &domainJWT.Key{
		ID:            kid,
		SigningKey:    signingKey,
		SigningMethod: signingMethod,
		VerifyingKey:  verifyingKey,
		VerifyingAlg:  verifyingMethod.Alg(),
	}, nil
}

// ParsePublicVerifyingKeys parses one or more PEM-encoded public keys (or multiple concatenated PEM blocks).
func ParsePublicVerifyingKeys(pemBytesList ...[]byte) ([]*domainJWT.Key, error) {
	var keys []*domainJWT.Key
	for _, pemBytes := range pemBytesList {
		rest := []byte(strings.TrimSpace(string(pemBytes)))
		for len(rest) > 0 {
			block, nextRest := pem.Decode(rest)
			if block == nil {
				break
			}
			blockBytes := pem.EncodeToMemory(block)
			vk, method, err := ParsePublicVerifyingKey(blockBytes)
			if err != nil {
				return nil, fmt.Errorf("parsing public key block: %w", err)
			}
			hash := sha256.Sum256(blockBytes)
			kid := hex.EncodeToString(hash[:8])
			keys = append(keys, &domainJWT.Key{
				ID:           kid,
				VerifyingKey: vk,
				VerifyingAlg: method.Alg(),
			})
			rest = nextRest
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid public key PEM blocks found")
	}
	return keys, nil
}

// ParsePrivateSigningKey parses a PEM-encoded RSA or ECDSA private key and
// returns the key together with the matching allowed signing method.
// HMAC and "none" are rejected so the algorithm cannot be confused with the key type.
func ParsePrivateSigningKey(pemBytes []byte) (any, jwt.SigningMethod, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, nil, fmt.Errorf("failed to decode PEM block for private key")
	}

	var parsed any
	var err error
	parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		parsed, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			parsed, err = x509.ParseECPrivateKey(block.Bytes)
			if err != nil {
				return nil, nil, fmt.Errorf("parsing private key: unsupported format")
			}
		}
	}

	method, err := signingMethodForPrivateKey(parsed)
	if err != nil {
		return nil, nil, err
	}
	return parsed, method, nil
}

// ParsePublicVerifyingKey parses a PEM-encoded RSA or ECDSA public key and
// returns the key together with the matching allowed signing method.
func ParsePublicVerifyingKey(pemBytes []byte) (any, jwt.SigningMethod, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, nil, fmt.Errorf("failed to decode PEM block for public key")
	}

	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		rsaKey, rsaErr := x509.ParsePKCS1PublicKey(block.Bytes)
		if rsaErr != nil {
			return nil, nil, fmt.Errorf("parsing public key: %w", err)
		}
		parsed = rsaKey
	}

	method, err := signingMethodForPublicKey(parsed)
	if err != nil {
		return nil, nil, err
	}
	return parsed, method, nil
}

func signingMethodForPrivateKey(key any) (jwt.SigningMethod, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return rsaSigningMethod(k.N.BitLen())
	case *ecdsa.PrivateKey:
		return ecdsaSigningMethod(k.Curve)
	default:
		return nil, fmt.Errorf("unsupported private key type %T (allowed: RSA, ECDSA)", key)
	}
}

func signingMethodForPublicKey(key any) (jwt.SigningMethod, error) {
	switch k := key.(type) {
	case *rsa.PublicKey:
		return rsaSigningMethod(k.N.BitLen())
	case *ecdsa.PublicKey:
		return ecdsaSigningMethod(k.Curve)
	default:
		return nil, fmt.Errorf("unsupported public key type %T (allowed: RSA, ECDSA)", key)
	}
}

func rsaSigningMethod(bits int) (jwt.SigningMethod, error) {
	if bits < minRSABits {
		return nil, fmt.Errorf("RSA key must be at least %d bits", minRSABits)
	}
	return jwt.SigningMethodRS256, nil
}

func ecdsaSigningMethod(curve elliptic.Curve) (jwt.SigningMethod, error) {
	switch curve {
	case elliptic.P256():
		return jwt.SigningMethodES256, nil
	case elliptic.P384():
		return jwt.SigningMethodES384, nil
	case elliptic.P521():
		return jwt.SigningMethodES512, nil
	default:
		name := "unknown"
		if curve != nil {
			name = curve.Params().Name
		}
		return nil, fmt.Errorf("unsupported ECDSA curve %s (allowed: P-256, P-384, P-521)", name)
	}
}
