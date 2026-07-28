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
	PrivateKeyPEM string `mapstructure:"jwt_private_key_pem" yaml:"jwt_private_key_pem"`
	PrivateKeyPath string `mapstructure:"jwt_private_key_path" yaml:"jwt_private_key_path"`
}

type JWTKeyManager struct {
	privateKey *rsa.PrivateKey
}

// NewJWTKeyManager loads and parses the RSA private key from configuration.
func NewJWTKeyManager(cfg JWTConfig) (*JWTKeyManager, error) {
	var keyBytes []byte

	if strings.TrimSpace(cfg.PrivateKeyPEM) != "" {
		keyBytes = []byte(cfg.PrivateKeyPEM)
	} else {
		keyPath := cfg.PrivateKeyPath
		if keyPath == "" {
			return nil, errors.New("[SECURITY CRITICAL] No JWT private key provided in config")
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
