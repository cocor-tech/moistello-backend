package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"

	"auth"
)

func generateTestPEM(t *testing.T) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}
	pemBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}
	return string(pem.EncodeToMemory(pemBlock))
}

func TestJWTKeyManager_Loading(t *testing.T) {
	testPEM := generateTestPEM(t)

	t.Run("successfully loads key from environment variable", func(t *testing.T) {
		t.Setenv("JWT_PRIVATE_KEY", testPEM)

		km, err := auth.NewJWTKeyManager(auth.JWTConfig{})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if km.GetPrivateKey() == nil {
			t.Fatal("expected non-nil private key")
		}
	})

	t.Run("fails at startup when no private key configuration is present", func(t *testing.T) {
		t.Setenv("JWT_PRIVATE_KEY", "")
		t.Setenv("JWT_PRIVATE_KEY_PATH", "")

		_, err := auth.NewJWTKeyManager(auth.JWTConfig{})
		if err == nil {
			t.Fatal("expected error when no JWT key source is provided, got nil")
		}
	})
} 