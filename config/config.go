package config

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server       ServerConfig
	Database     DatabaseConfig
	Redis        RedisConfig
	RabbitMQ     RabbitMQConfig
	Stellar      StellarConfig
	Auth         AuthConfig
	Security     SecurityConfig
	Brevo        BrevoConfig
	Indexer      IndexerConfig
	Notification NotificationConfig
	CORS         CORSConfig
	RateLimit    RateLimitConfig `mapstructure:"rate_limit"`
	Logging      LoggingConfig
	Environment  string
	YellowCard   YellowCardConfig `mapstructure:"yellow_card"`
	Tracing      TracingConfig
	Swap         SwapConfig `mapstructure:"swap"`
}

type ServerConfig struct {
	Port             int           `mapstructure:"port"`
	Host             string        `mapstructure:"host"`
	ReadTimeout      time.Duration `mapstructure:"read_timeout"`
	WriteTimeout     time.Duration `mapstructure:"write_timeout"`
	MaxHeaderBytes   int           `mapstructure:"max_header_bytes"`
	TLSEnabled       bool          `mapstructure:"tls_enabled"`
	TLSCertPath      string        `mapstructure:"tls_cert_path"`
	TLSKeyPath       string        `mapstructure:"tls_key_path"`
	HTTPRedirectPort int           `mapstructure:"http_redirect_port"`
}

type DatabaseConfig struct {
	URL             string        `mapstructure:"url"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	MigrationPath   string        `mapstructure:"migration_path"`
}

type RedisConfig struct {
	URL      string `mapstructure:"url"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
}

type RabbitMQConfig struct {
	URL      string `mapstructure:"url"`
	Exchange string `mapstructure:"exchange"`
	Queues   struct {
		Notifications string `mapstructure:"notifications"`
		Webhooks      string `mapstructure:"webhooks"`
	} `mapstructure:"queues"`
}

type StellarConfig struct {
	Network                   string  `mapstructure:"network"`
	HorizonURL                string  `mapstructure:"horizon_url"`
	SorobanRPCURL             string  `mapstructure:"soroban_rpc_url"`
	NetworkPassphrase         string  `mapstructure:"network_passphrase"`
	MasterPublicKey           string  `mapstructure:"master_public_key"`
	MasterSecretKey           string  `mapstructure:"master_secret_key"`
	USDCIssuer                string  `mapstructure:"usdc_issuer"`
	WalletMinBalance          float64 `mapstructure:"wallet_min_balance"`
	GovernanceTokenContractID string  `mapstructure:"governance_token_contract_id"`
	EscrowSwapContractID      string  `mapstructure:"escrow_swap_contract_id"`
}

func (s StellarConfig) MarshalJSON() ([]byte, error) {
	type alias StellarConfig
	return json.Marshal(&struct{ alias }{alias: alias(s)})
}

func (s StellarConfig) String() string {
	if s.MasterSecretKey != "" {
		return "{... redacted ...}"
	}
	return "{...}"
}

type YellowCardConfig struct {
	APIKey               string  `mapstructure:"api_key"`
	APISecret            string  `mapstructure:"api_secret"`
	MaxDepositNGN        float64 `mapstructure:"max_deposit_ngn"`
	MaxWithdrawUSDC      float64 `mapstructure:"max_withdraw_usdc"`
	DailyDepositCapNGN   float64 `mapstructure:"daily_deposit_cap_ngn"`
	DailyWithdrawCapUSDC float64 `mapstructure:"daily_withdraw_cap_usdc"`
}

type AuthConfig struct {
	JWTPrivateKeyPath string        `mapstructure:"jwt_private_key_path"`
	JWTPublicKeyPath  string        `mapstructure:"jwt_public_key_path"`
	JWTPrivateKeyPEM  string        `mapstructure:"jwt_private_key_pem"`
	JWTPublicKeyPEM   string        `mapstructure:"jwt_public_key_pem"`
	AccessTokenTTL    time.Duration `mapstructure:"access_token_ttl"`
	RefreshTokenTTL   time.Duration `mapstructure:"refresh_token_ttl"`
	NonceTTL          time.Duration `mapstructure:"nonce_ttl"`
	AdminAPIKey       string        `mapstructure:"admin_api_key"`
}

type SecurityConfig struct {
	// WalletPepper is the single server-side secret used for deterministic
	// wallet seed derivation. The former, unused PasskeyPepper field and its
	// MOISTELLO_PASSKEY_PEPPER env binding were removed in #163 so there is
	// exactly one pepper source of truth.
	WalletPepper  string `mapstructure:"wallet_pepper"`
	EncryptionKey string `mapstructure:"encryption_key"`
	Argon2Time    int    `mapstructure:"argon2_time"`
	Argon2Memory  int    `mapstructure:"argon2_memory"`
	Argon2Threads int    `mapstructure:"argon2_threads"`
}

type BrevoConfig struct {
	APIKey    string `mapstructure:"api_key"`
	FromEmail string `mapstructure:"from_email"`
	FromName  string `mapstructure:"from_name"`
}

type IndexerConfig struct {
	PollInterval time.Duration `mapstructure:"poll_interval"`
	BatchSize    int           `mapstructure:"batch_size"`
	StartLedger  int64         `mapstructure:"start_ledger"`
}

type NotificationConfig struct {
	Email struct {
		Provider    string `mapstructure:"provider"`
		APIKey      string `mapstructure:"api_key"`
		FromAddress string `mapstructure:"from_address"`
	} `mapstructure:"email"`
	SMS struct {
		Provider   string `mapstructure:"provider"`
		AccountSID string `mapstructure:"account_sid"`
		AuthToken  string `mapstructure:"auth_token"`
		FromNumber string `mapstructure:"from_number"`
	} `mapstructure:"sms"`
	Push struct {
		FCMServerKey string `mapstructure:"fcm_server_key"`
	} `mapstructure:"push"`
}

type CORSConfig struct {
	AllowedOrigins   []string      `mapstructure:"allowed_origins"`
	AllowedMethods   []string      `mapstructure:"allowed_methods"`
	AllowedHeaders   []string      `mapstructure:"allowed_headers"`
	AllowCredentials bool          `mapstructure:"allow_credentials"`
	MaxAge           time.Duration `mapstructure:"max_age"`
}

type SwapConfig struct {
	// SweepInterval is how often the swap sweep worker runs. The sweep
	// releases escrow on-chain for created swap offers past their expiry and
	// marks them expired (#243).
	SweepInterval time.Duration `mapstructure:"sweep_interval"`
}

type RateLimitConfig struct {
	Global        int `mapstructure:"global"`
	Authenticated int `mapstructure:"authenticated"`
	Auth          int `mapstructure:"auth"`
	// FailClosed decides what happens when Redis is unreachable during a rate
	// limit check. True (the default, and the single policy documented in
	// docs/rate-limiting.md) refuses the request with 503 — the same posture
	// the legacy JS middleware/rateLimiter.js always had. False falls back to
	// the in-memory limiter (fails open). Individual routes may override this
	// per-route via middleware.RateLimitMiddleware options.
	FailClosed bool `mapstructure:"fail_closed"`

	// Per-resource limits (#197). middleware.PerResourceRateLimitMiddleware
	// existed but was never applied to any route — these give the
	// high-value/sensitive resources their own configurable, tighter buckets
	// instead of sharing the coarse Global/Authenticated/Auth limits above.
	OTPLimit                    int `mapstructure:"otp_limit"`
	OTPWindowSeconds            int `mapstructure:"otp_window_seconds"`
	SwapLimit                   int `mapstructure:"swap_limit"`
	SwapWindowSeconds           int `mapstructure:"swap_window_seconds"`
	ContributeLimit             int `mapstructure:"contribute_limit"`
	ContributeWindowSeconds     int `mapstructure:"contribute_window_seconds"`
	WalletTransferLimit         int `mapstructure:"wallet_transfer_limit"`
	WalletTransferWindowSeconds int `mapstructure:"wallet_transfer_window_seconds"`
	ReferralLimit               int `mapstructure:"referral_limit"`
	ReferralWindowSeconds       int `mapstructure:"referral_window_seconds"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
}

type TracingConfig struct {
	Enabled           bool    `mapstructure:"enabled"`
	CollectorEndpoint string  `mapstructure:"collector_endpoint"`
	ServiceName       string  `mapstructure:"service_name"`
	SampleRate        float64 `mapstructure:"sample_rate"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("./config")
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/moistello/")
	v.SetEnvPrefix("MOISTELLO")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	setDefault(v, "server.port", 1100)
	setDefault(v, "server.host", "0.0.0.0")
	setDefault(v, "server.read_timeout", "10s")
	setDefault(v, "server.write_timeout", "30s")
	setDefault(v, "server.max_header_bytes", 1048576)
	setDefault(v, "server.tls_enabled", false)
	setDefault(v, "server.http_redirect_port", 80)
	setDefault(v, "database.max_open_conns", 50)
	setDefault(v, "database.max_idle_conns", 10)
	setDefault(v, "database.conn_max_lifetime", "30m")
	setDefault(v, "redis.url", "redis://localhost:6379")
	setDefault(v, "redis.pool_size", 20)
	setDefault(v, "rabbitmq.url", "amqp://guest:guest@localhost:5672/")
	setDefault(v, "rabbitmq.exchange", "moistello.events")
	setDefault(v, "rabbitmq.queues.notifications", "moistello.notifications")
	setDefault(v, "rabbitmq.queues.webhooks", "moistello.webhooks")
	setDefault(v, "stellar.network", "testnet")
	setDefault(v, "stellar.horizon_url", "https://horizon-testnet.stellar.org")
	setDefault(v, "stellar.soroban_rpc_url", "https://soroban-testnet.stellar.org")
	setDefault(v, "stellar.network_passphrase", "Test SDF Network ; September 2015")
	setDefault(v, "stellar.usdc_issuer", "GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC")
	setDefault(v, "stellar.wallet_min_balance", 10.0)
	setDefault(v, "auth.access_token_ttl", "15m")
	setDefault(v, "auth.refresh_token_ttl", "168h")
	setDefault(v, "auth.nonce_ttl", "5m")
	setDefault(v, "auth.jwt_private_key_path", "./config/keys/jwt-private.pem")
	setDefault(v, "auth.jwt_public_key_path", "./config/keys/jwt-public.pem")
	setDefault(v, "security.argon2_time", 1)
	setDefault(v, "security.argon2_memory", 64*1024)
	setDefault(v, "security.argon2_threads", 4)
	setDefault(v, "brevo.api_key", "")
	setDefault(v, "brevo.from_email", "noreply@moistello.com")
	setDefault(v, "brevo.from_name", "Moistello")
	setDefault(v, "indexer.poll_interval", "3s")
	setDefault(v, "indexer.batch_size", 50)
	setDefault(v, "cors.allowed_origins", []string{"http://localhost:1110"})
	setDefault(v, "cors.allowed_methods", []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"})
	setDefault(v, "cors.allowed_headers", []string{"Authorization", "Content-Type", "X-Request-ID"})
	setDefault(v, "cors.allow_credentials", true)
	setDefault(v, "cors.max_age", "24h")
	setDefault(v, "rate_limit.global", 100)
	setDefault(v, "rate_limit.authenticated", 300)
	setDefault(v, "rate_limit.auth", 10)
	setDefault(v, "rate_limit.fail_closed", true)
	setDefault(v, "rate_limit.otp_limit", 5)
	setDefault(v, "rate_limit.otp_window_seconds", 900)
	setDefault(v, "rate_limit.swap_limit", 10)
	setDefault(v, "rate_limit.swap_window_seconds", 60)
	setDefault(v, "rate_limit.contribute_limit", 10)
	setDefault(v, "rate_limit.contribute_window_seconds", 60)
	setDefault(v, "rate_limit.wallet_transfer_limit", 5)
	setDefault(v, "rate_limit.wallet_transfer_window_seconds", 60)
	setDefault(v, "rate_limit.referral_limit", 10)
	setDefault(v, "rate_limit.referral_window_seconds", 3600)
	setDefault(v, "logging.level", "debug")
	setDefault(v, "logging.format", "json")
	setDefault(v, "logging.output", "stdout")
	setDefault(v, "environment", "development")
	setDefault(v, "notification.email.provider", "")
	setDefault(v, "notification.email.api_key", "")
	setDefault(v, "notification.email.from_address", "")
	setDefault(v, "notification.sms.provider", "")
	setDefault(v, "notification.sms.account_sid", "")
	setDefault(v, "notification.sms.auth_token", "")
	setDefault(v, "notification.sms.from_number", "")
	setDefault(v, "notification.push.fcm_server_key", "")
	setDefault(v, "yellow_card.api_key", "")
	setDefault(v, "yellow_card.api_secret", "")
	setDefault(v, "swap.sweep_interval", "1m")
	setDefault(v, "security.wallet_pepper", "")
	setDefault(v, "security.passkey_pepper", "")
	setDefault(v, "security.encryption_key", "")

	mustBindEnv(v, "environment", "MOISTELLO_ENVIRONMENT", "NODE_ENV")
	mustBindEnv(v, "database.url", "MOISTELLO_DATABASE_URL", "DATABASE_URL")
	mustBindEnv(v, "stellar.master_secret_key", "MOISTELLO_STELLAR_MASTER_SECRET_KEY", "STELLAR_MASTER_SECRET_KEY")
	mustBindEnv(v, "stellar.master_public_key", "MOISTELLO_STELLAR_MASTER_PUBLIC_KEY", "STELLAR_MASTER_PUBLIC_KEY")
	mustBindEnv(v, "security.wallet_pepper", "MOISTELLO_WALLET_PEPPER")
	mustBindEnv(v, "security.encryption_key", "ENCRYPTION_KEY")
	mustBindEnv(v, "auth.jwt_private_key_pem", "JWT_PRIVATE_KEY")
	mustBindEnv(v, "auth.jwt_public_key_pem", "JWT_PUBLIC_KEY")
	mustBindEnv(v, "brevo.api_key", "MOISTELLO_BREVO_API_KEY", "MOISTELLO_NOTIFICATION_EMAIL_APIKEY", "MOISTELLO_EMAIL_API_KEY")
	mustBindEnv(v, "brevo.from_email", "MOISTELLO_BREVO_FROM_EMAIL", "MOISTELLO_NOTIFICATION_EMAIL_FROM_ADDRESS")
	mustBindEnv(v, "brevo.from_name", "MOISTELLO_BREVO_FROM_NAME", "MOISTELLO_NOTIFICATION_EMAIL_FROM_NAME")
	mustBindEnv(v, "yellow_card.api_key", "YELLOW_CARD_API_KEY")
	mustBindEnv(v, "yellow_card.api_secret", "YELLOW_CARD_API_SECRET")
	v.SetDefault("server.port", 1100)
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.read_timeout", "10s")
	v.SetDefault("server.write_timeout", "30s")
	v.SetDefault("server.max_header_bytes", 1048576)
	v.SetDefault("server.tls_enabled", false)
	v.SetDefault("server.http_redirect_port", 80)
	// No default DATABASE_URL: the env var DATABASE_URL (mapped to MOISTELLO_DATABASE_URL)
	// must be set explicitly. An empty URL will cause a clear startup failure rather than
	// silently connecting with plaintext credentials and SSL disabled.
	v.SetDefault("database.max_open_conns", 50)
	v.SetDefault("database.max_idle_conns", 10)
	v.SetDefault("database.conn_max_lifetime", "30m")
	v.SetDefault("redis.url", "redis://localhost:6379")
	v.SetDefault("redis.pool_size", 20)
	v.SetDefault("rabbitmq.url", "amqp://guest:guest@localhost:5672/")
	v.SetDefault("rabbitmq.exchange", "moistello.events")
	v.SetDefault("rabbitmq.queues.notifications", "moistello.notifications")
	v.SetDefault("rabbitmq.queues.webhooks", "moistello.webhooks")
	v.SetDefault("stellar.network", "testnet")
	v.SetDefault("stellar.horizon_url", "https://horizon-testnet.stellar.org")
	v.SetDefault("stellar.soroban_rpc_url", "https://soroban-testnet.stellar.org")
	v.SetDefault("stellar.network_passphrase", "Test SDF Network ; September 2015")
	v.SetDefault("stellar.governance_token_contract_id", "")
	v.SetDefault("stellar.escrow_swap_contract_id", "")
	v.SetDefault("auth.access_token_ttl", "15m")
	v.SetDefault("auth.refresh_token_ttl", "168h")
	v.SetDefault("auth.nonce_ttl", "5m")
	v.SetDefault("brevo.api_key", "")
	v.SetDefault("brevo.from_email", "noreply@moistello.com")
	v.SetDefault("brevo.from_name", "Moistello")
	v.SetDefault("indexer.poll_interval", "3s")
	v.SetDefault("indexer.batch_size", 50)
	v.SetDefault("cors.allowed_origins", []string{"http://localhost:1110"})
	v.SetDefault("cors.allowed_methods", []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"})
	v.SetDefault("cors.allowed_headers", []string{"Authorization", "Content-Type", "X-Request-ID"})
	v.SetDefault("cors.allow_credentials", true)
	v.SetDefault("cors.max_age", "24h")
	v.SetDefault("rate_limit.global", 100)
	v.SetDefault("rate_limit.authenticated", 300)
	v.SetDefault("rate_limit.auth", 10)
	v.SetDefault("rate_limit.fail_closed", true)
	v.SetDefault("rate_limit.otp_limit", 5)
	v.SetDefault("rate_limit.otp_window_seconds", 900)
	v.SetDefault("rate_limit.swap_limit", 10)
	v.SetDefault("rate_limit.swap_window_seconds", 60)
	v.SetDefault("rate_limit.contribute_limit", 10)
	v.SetDefault("rate_limit.contribute_window_seconds", 60)
	v.SetDefault("rate_limit.wallet_transfer_limit", 5)
	v.SetDefault("rate_limit.wallet_transfer_window_seconds", 60)
	v.SetDefault("rate_limit.referral_limit", 10)
	v.SetDefault("rate_limit.referral_window_seconds", 3600)
	v.SetDefault("logging.level", "debug")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "stdout")
	v.SetDefault("tracing.enabled", false)
	v.SetDefault("tracing.collector_endpoint", "localhost:4317")
	v.SetDefault("tracing.service_name", "moistello-api")
	v.SetDefault("tracing.sample_rate", 0.1)
	v.SetDefault("environment", "development")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			panic(fmt.Errorf("config: reading config file: %w", err))
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		panic(fmt.Errorf("config: unmarshaling config: %w", err))
	}

	cfg.Environment = strings.TrimSpace(v.GetString("environment"))
	cfg.Database.URL = requireString("database.url", cfg.Database.URL, "set MOISTELLO_DATABASE_URL or DATABASE_URL")
	cfg.Stellar.MasterSecretKey = requireString("stellar.master_secret_key", cfg.Stellar.MasterSecretKey, "set MOISTELLO_STELLAR_MASTER_SECRET_KEY or STELLAR_MASTER_SECRET_KEY")
	cfg.Stellar.MasterPublicKey = requireString("stellar.master_public_key", cfg.Stellar.MasterPublicKey, "set MOISTELLO_STELLAR_MASTER_PUBLIC_KEY or STELLAR_MASTER_PUBLIC_KEY")
	cfg.Security.WalletPepper = requireString("security.wallet_pepper", cfg.Security.WalletPepper, "set MOISTELLO_WALLET_PEPPER")
	cfg.Security.EncryptionKey = requireString("security.encryption_key", cfg.Security.EncryptionKey, "set ENCRYPTION_KEY")

	cfg.Auth.JWTPrivateKeyPEM = loadRequiredText(cfg.Auth.JWTPrivateKeyPEM, cfg.Auth.JWTPrivateKeyPath, "auth.jwt_private_key_pem", "auth.jwt_private_key_path")
	cfg.Auth.JWTPublicKeyPEM = loadRequiredText(cfg.Auth.JWTPublicKeyPEM, cfg.Auth.JWTPublicKeyPath, "auth.jwt_public_key_pem", "auth.jwt_public_key_path")

	validateHexKey(cfg.Security.EncryptionKey)
	validateDuration("security.argon2_time", cfg.Security.Argon2Time > 0)
	validateDuration("security.argon2_memory", cfg.Security.Argon2Memory > 0)
	validateDuration("security.argon2_threads", cfg.Security.Argon2Threads > 0)

	if cfg.Environment != "development" && strings.Contains(cfg.Database.URL, "sslmode=disable") {
		panic(fmt.Errorf("database.url must not use sslmode=disable outside development; use sslmode=require or stronger"))
	}

	return &cfg, nil
}

func loadDotEnv() {
	for _, candidate := range []string{".env", ".env.local", "config/.env"} {
		if err := loadEnvFile(candidate); err != nil {
			panic(fmt.Errorf("config: loading %s: %w", candidate, err))
		}
	}
}

func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if len(value) >= 2 {
			if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) || (strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
				value = value[1 : len(value)-1]
			}
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func mustBindEnv(v *viper.Viper, key string, envNames ...string) {
	args := append([]string{key}, envNames...)
	if err := v.BindEnv(args...); err != nil {
		panic(fmt.Errorf("config: binding env for %s: %w", key, err))
	}
}

func setDefault(v *viper.Viper, key string, value any) {
	v.SetDefault(key, value)
}

func requireString(field, value, hint string) string {
	if strings.TrimSpace(value) == "" {
		panic(fmt.Errorf("config: %s is required (%s)", field, hint))
	}
	return strings.TrimSpace(value)
}

func loadRequiredText(current, path, field, pathField string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	if strings.TrimSpace(path) == "" {
		panic(fmt.Errorf("config: %s is required (set it directly or configure %s)", field, pathField))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		panic(fmt.Errorf("config: reading %s from %q: %w", field, path, err))
	}
	return strings.TrimSpace(string(data))
}

func validateHexKey(value string) {
	raw, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil || len(raw) != 32 {
		panic(fmt.Errorf("config: security.encryption_key must be a 32-byte hex string"))
	}
}

func validateDuration(name string, ok bool) {
	if !ok {
		panic(fmt.Errorf("config: %s must be greater than zero", name))
	}
}
