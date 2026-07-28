package config_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/config"
)

func TestLoad_SucceedsWithRequiredConfig(t *testing.T) {
	t.Setenv("MOISTELLO_DATABASE_URL", "postgres://localhost:5432/db")
	t.Setenv("MOISTELLO_STELLAR_MASTER_SECRET_KEY", "SAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("MOISTELLO_STELLAR_MASTER_PUBLIC_KEY", "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("MOISTELLO_WALLET_PEPPER", "wallet-pepper")
	t.Setenv("MOISTELLO_PASSKEY_PEPPER", "passkey-pepper")
	t.Setenv("ENCRYPTION_KEY", hex.EncodeToString([]byte("12345678901234567890123456789012")))
	t.Setenv("JWT_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALs=\n-----END RSA PRIVATE KEY-----")
	t.Setenv("JWT_PUBLIC_KEY", "-----BEGIN RSA PUBLIC KEY-----\nMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALs=\n-----END RSA PUBLIC KEY-----")

	cfg := config.Load()
	require.NotNil(t, cfg)
	require.Equal(t, "postgres://localhost:5432/db", cfg.Database.URL)
	require.Equal(t, "wallet-pepper", cfg.Security.WalletPepper)
	require.Equal(t, "passkey-pepper", cfg.Security.PasskeyPepper)
	require.NotEmpty(t, cfg.Auth.JWTPrivateKeyPEM)
	require.NotEmpty(t, cfg.Auth.JWTPublicKeyPEM)
}

func TestLoad_PanicsWithoutCriticalConfig(t *testing.T) {
	t.Setenv("MOISTELLO_DATABASE_URL", "")
	t.Setenv("MOISTELLO_STELLAR_MASTER_SECRET_KEY", "")
	t.Setenv("MOISTELLO_STELLAR_MASTER_PUBLIC_KEY", "")
	t.Setenv("MOISTELLO_WALLET_PEPPER", "")
	t.Setenv("MOISTELLO_PASSKEY_PEPPER", "")
	t.Setenv("ENCRYPTION_KEY", "")
	t.Setenv("JWT_PRIVATE_KEY", "")
	t.Setenv("JWT_PUBLIC_KEY", "")

	require.Panics(t, func() {
		config.Load()
	})
}
