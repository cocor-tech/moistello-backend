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

// setRequiredConfigEnv provides the secrets config.Load insists on, so an
// assertion about a single setting fails on that setting rather than on
// unrelated startup validation.
func setRequiredConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MOISTELLO_DATABASE_URL", "postgres://localhost:5432/db")
	t.Setenv("MOISTELLO_STELLAR_MASTER_SECRET_KEY", "SAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("MOISTELLO_STELLAR_MASTER_PUBLIC_KEY", "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("MOISTELLO_WALLET_PEPPER", "wallet-pepper")
	t.Setenv("ENCRYPTION_KEY", hex.EncodeToString([]byte("12345678901234567890123456789012")))
	t.Setenv("JWT_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALs=\n-----END RSA PRIVATE KEY-----")
	t.Setenv("JWT_PUBLIC_KEY", "-----BEGIN RSA PUBLIC KEY-----\nMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALs=\n-----END RSA PUBLIC KEY-----")
}

// indexer.reorg_window bounds how deep a ledger reorganization is repaired
// (#346). It must be settable and must have a safe default.
func TestLoad_ReorgWindowIsConfigurable(t *testing.T) {
	setRequiredConfigEnv(t)
	t.Setenv("MOISTELLO_INDEXER_REORG_WINDOW", "25")

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.Equal(t, 25, cfg.Indexer.ReorgWindow)
}

func TestLoad_ReorgWindowHasDefault(t *testing.T) {
	setRequiredConfigEnv(t)

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.Equal(t, 10, cfg.Indexer.ReorgWindow)
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

func TestValidateSecrets_FailsFastOnMissingSecrets(t *testing.T) {
	cfg := &config.Config{}
	err := cfg.ValidateSecrets()
	require.Error(t, err)
	errStr := err.Error()
	require.Contains(t, errStr, "Missing required secrets")
	require.Contains(t, errStr, "Auth.JWTPrivateKeyPEM")
	require.Contains(t, errStr, "Auth.JWTPublicKeyPEM")
	require.Contains(t, errStr, "Auth.AdminAPIKey")
	require.Contains(t, errStr, "Security.WalletPepper")
	require.Contains(t, errStr, "Security.EncryptionKey")
	require.Contains(t, errStr, "Stellar.MasterSecretKey")
	require.Contains(t, errStr, "Redis.Password")
	require.Contains(t, errStr, "Database.URL")
	require.Contains(t, errStr, "YellowCard.WebhookSecret")
}

func TestValidateSecrets_RejectsNonHexAndWrongLengthSecrets(t *testing.T) {
	hexKey := hex.EncodeToString([]byte("12345678901234567890123456789012"))
	cfg := &config.Config{}
	cfg.Auth.JWTPrivateKeyPEM = "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALs=\n-----END RSA PRIVATE KEY-----"
	cfg.Auth.JWTPublicKeyPEM = "-----BEGIN RSA PUBLIC KEY-----\nMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALs=\n-----END RSA PUBLIC KEY-----"
	cfg.Auth.AdminAPIKey = "not-a-valid-hex-admin-key-with-non-hex-letters"
	cfg.Security.WalletPepper = "short"
	cfg.Security.EncryptionKey = hexKey
	cfg.Stellar.MasterSecretKey = "SAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	cfg.Redis.Password = "redis-password-123456"
	cfg.Database.URL = "postgres://localhost:5432/db"
	cfg.YellowCard.WebhookSecret = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"

	err := cfg.ValidateSecrets()
	require.Error(t, err)
	errStr := err.Error()
	require.Contains(t, errStr, "Auth.AdminAPIKey must be 32-byte hex")
	require.Contains(t, errStr, "Security.WalletPepper must be 32-byte hex")
	require.Contains(t, errStr, "YellowCard.WebhookSecret must be 32-byte hex")
}

func TestLoad_RejectsPlaceholderSecretsAndPartialPreviousJWTKeys(t *testing.T) {
	setRequiredConfigEnv(t)
	t.Setenv("MOISTELLO_WALLET_PEPPER", "changeme")
	t.Setenv("JWT_PREVIOUS_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALs=\n-----END RSA PRIVATE KEY-----")
	t.Setenv("JWT_PREVIOUS_PUBLIC_KEY", "")

	cfg, err := config.Load("")
	require.Error(t, err)
	require.Nil(t, cfg)
	errStr := err.Error()
	require.Contains(t, errStr, "security.wallet_pepper must not use a placeholder value")
	require.Contains(t, errStr, "auth.jwt_previous_private_key_pem and auth.jwt_previous_public_key_pem must both be set")
}
