package config_test

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/config"
)

func TestLoad_SucceedsWithRequiredConfig(t *testing.T) {
	t.Setenv("MOISTELLO_DATABASE_URL", "postgres://localhost:5432/db")
	t.Setenv("MOISTELLO_STELLAR_MASTER_SECRET_KEY", "SAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("MOISTELLO_STELLAR_MASTER_PUBLIC_KEY", "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("MOISTELLO_WALLET_PEPPER", "wallet-pepper")
	t.Setenv("ENCRYPTION_KEY", hex.EncodeToString([]byte("12345678901234567890123456789012")))
	t.Setenv("JWT_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALs=\n-----END RSA PRIVATE KEY-----")
	t.Setenv("JWT_PUBLIC_KEY", "-----BEGIN RSA PUBLIC KEY-----\nMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALs=\n-----END RSA PUBLIC KEY-----")

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "postgres://localhost:5432/db", cfg.Database.URL)
	require.Equal(t, "wallet-pepper", cfg.Security.WalletPepper)
	require.NotEmpty(t, cfg.Auth.JWTPrivateKeyPEM)
	require.NotEmpty(t, cfg.Auth.JWTPublicKeyPEM)
	require.Equal(t, 30*time.Second, cfg.Server.ShutdownTimeout)
	require.Equal(t, time.Duration(0), cfg.Server.ShutdownDelay)
}

func TestLoad_ShutdownTimeoutIsConfigurable(t *testing.T) {
	t.Setenv("MOISTELLO_DATABASE_URL", "postgres://localhost:5432/db")
	t.Setenv("MOISTELLO_STELLAR_MASTER_SECRET_KEY", "SAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("MOISTELLO_STELLAR_MASTER_PUBLIC_KEY", "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("MOISTELLO_WALLET_PEPPER", "wallet-pepper")
	t.Setenv("ENCRYPTION_KEY", hex.EncodeToString([]byte("12345678901234567890123456789012")))
	t.Setenv("JWT_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALs=\n-----END RSA PRIVATE KEY-----")
	t.Setenv("JWT_PUBLIC_KEY", "-----BEGIN RSA PUBLIC KEY-----\nMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALs=\n-----END RSA PUBLIC KEY-----")
	t.Setenv("MOISTELLO_SERVER_SHUTDOWN_TIMEOUT", "5s")
	t.Setenv("MOISTELLO_SERVER_SHUTDOWN_DELAY", "2s")

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, cfg.Server.ShutdownTimeout)
	require.Equal(t, 2*time.Second, cfg.Server.ShutdownDelay)
}

func TestLoad_ReportsAllMissingErrors(t *testing.T) {
	t.Setenv("MOISTELLO_DATABASE_URL", "")
	t.Setenv("MOISTELLO_STELLAR_MASTER_SECRET_KEY", "")
	t.Setenv("MOISTELLO_STELLAR_MASTER_PUBLIC_KEY", "")
	t.Setenv("MOISTELLO_WALLET_PEPPER", "")
	t.Setenv("ENCRYPTION_KEY", "")
	t.Setenv("JWT_PRIVATE_KEY", "")
	t.Setenv("JWT_PUBLIC_KEY", "")

	cfg, err := config.Load("")
	require.Error(t, err)
	require.Nil(t, cfg)
	errStr := err.Error()
	require.Contains(t, errStr, "database.url is required")
	require.Contains(t, errStr, "stellar.master_secret_key is required")
	require.Contains(t, errStr, "stellar.master_public_key is required")
	require.Contains(t, errStr, "security.wallet_pepper is required")
	require.Contains(t, errStr, "security.encryption_key is required")
	require.Contains(t, errStr, "jwt_private_key_pem")
	require.Contains(t, errStr, "jwt_public_key_pem")
}

func TestValidateOffline_Succeeds(t *testing.T) {
	hexKey := hex.EncodeToString([]byte("12345678901234567890123456789012"))
	t.Setenv("MOISTELLO_DATABASE_URL", "postgres://localhost:5432/db")
	t.Setenv("MOISTELLO_STELLAR_MASTER_SECRET_KEY", "SAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("MOISTELLO_STELLAR_MASTER_PUBLIC_KEY", "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("MOISTELLO_WALLET_PEPPER", hexKey)
	t.Setenv("ENCRYPTION_KEY", hexKey)
	t.Setenv("ADMIN_API_KEY", hexKey)
	t.Setenv("REDIS_PASSWORD", "redis-password-123456")
	t.Setenv("YELLOW_CARD_WEBHOOK_SECRET", hexKey)
	t.Setenv("JWT_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALs=\n-----END RSA PRIVATE KEY-----")
	t.Setenv("JWT_PUBLIC_KEY", "-----BEGIN RSA PUBLIC KEY-----\nMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALs=\n-----END RSA PUBLIC KEY-----")

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.NotNil(t, cfg)

	err = cfg.ValidateOffline()
	require.NoError(t, err)
}

