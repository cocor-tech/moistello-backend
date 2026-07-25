package auth

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type JWTConfig struct {
	PrivateKeyPEM string `mapstructure:"jwt_private_key" yaml:"jwt_private_key"`
	PrivateKeyPath string `mapstructure:"jwt_private_key_path" yaml:"jwt_private_key_path"`
}

type JWTKeyManager struct {
	privateKey *rsa.PrivateKey
}

// NewJWTKeyManager loads and parses the RSA private key securely from environment variables
// or, as a secondary fallback, from a secure file path.
func NewJWTKeyManager(cfg JWTConfig) (*JWTKeyManager, error) {
	var keyBytes []byte

	// 1. Prefer reading directly from raw PEM environment variable
	if pemEnv := strings.TrimSpace(os.Getenv("JWT_PRIVATE_KEY")); pemEnv != "" {
		keyBytes = []byte(pemEnv)
	} else if strings.TrimSpace(cfg.PrivateKeyPEM) != "" {
		keyBytes = []byte(cfg.PrivateKeyPEM)
	} else {
		// 2. Fallback to file path configuration if explicitly specified
		keyPath := cfg.PrivateKeyPath
		if keyPath == "" {
			keyPath = os.Getenv("JWT_PRIVATE_KEY_PATH")
		}

		if keyPath == "" {
			return nil, errors.New("[SECURITY CRITICAL] No JWT private key provided. Set JWT_PRIVATE_KEY environment variable")
		}

		var err error
		keyBytes, err = os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read JWT private key file at %s: %w", keyPath, err)
		}
	}

	// 3. Parse RSA Private Key
	parsedKey, err := jwt.ParseRSAPrivateKeyFromPEM(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid RSA private key format: %w", err)
	}

	return &JWTKeyManager{privateKey: parsedKey}, nil
}

func (m *JWTKeyManager) GetPrivateKey() *rsa.PrivateKey {
	return m.privateKey
}