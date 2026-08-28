package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

const minRSABits = 2048

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
