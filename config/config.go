package config

import (
	"fmt"
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
	Brevo        BrevoConfig
	Indexer      IndexerConfig
	Notification NotificationConfig
	CORS         CORSConfig
	RateLimit    RateLimitConfig `mapstructure:"rate_limit"`
	Logging      LoggingConfig
	Environment  string
}

type ServerConfig struct {
	Port           int           `mapstructure:"port"`
	Host           string        `mapstructure:"host"`
	ReadTimeout    time.Duration `mapstructure:"read_timeout"`
	WriteTimeout   time.Duration `mapstructure:"write_timeout"`
	MaxHeaderBytes int           `mapstructure:"max_header_bytes"`
}

type DatabaseConfig struct {
	URL            string        `mapstructure:"url"`
	MaxOpenConns   int           `mapstructure:"max_open_conns"`
	MaxIdleConns   int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	MigrationPath  string        `mapstructure:"migration_path"`
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
	Network           string `mapstructure:"network"`
	HorizonURL        string `mapstructure:"horizon_url"`
	SorobanRPCURL     string `mapstructure:"soroban_rpc_url"`
	NetworkPassphrase string `mapstructure:"network_passphrase"`
	MasterPublicKey   string `mapstructure:"master_public_key"`
	MasterSecretKey   string `mapstructure:"master_secret_key"`
	USDCIssuer        string `mapstructure:"usdc_issuer"`
	WalletMinBalance  float64 `mapstructure:"wallet_min_balance"`
}

type AuthConfig struct {
	JWTPrivateKeyPath string        `mapstructure:"jwt_private_key_path"`
	JWTPublicKeyPath  string        `mapstructure:"jwt_public_key_path"`
	AccessTokenTTL    time.Duration `mapstructure:"access_token_ttl"`
	RefreshTokenTTL   time.Duration `mapstructure:"refresh_token_ttl"`
	NonceTTL          time.Duration `mapstructure:"nonce_ttl"`
}

type BrevoConfig struct {
	APIKey      string `mapstructure:"api_key"`
	FromEmail   string `mapstructure:"from_email"`
	FromName    string `mapstructure:"from_name"`
}

type IndexerConfig struct {
	PollInterval time.Duration `mapstructure:"poll_interval"`
	BatchSize    int           `mapstructure:"batch_size"`
	StartLedger  int64         `mapstructure:"start_ledger"`
}

type NotificationConfig struct {
	Email struct {
		Provider    string `mapstructure:"provider"`
		APIKey      string `mapstructure:"apiKey"`
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

type RateLimitConfig struct {
	Global        int `mapstructure:"global"`
	Authenticated int `mapstructure:"authenticated"`
	Auth          int `mapstructure:"auth"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(path)
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/moistello/")
	v.SetEnvPrefix("MOISTELLO")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	v.SetDefault("server.port", 1100)
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.read_timeout", "10s")
	v.SetDefault("server.write_timeout", "30s")
	v.SetDefault("server.max_header_bytes", 1048576)
	v.SetDefault("database.url", "postgres://moistello:moistello_dev@localhost:9811/moistello?sslmode=disable")
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
	v.SetDefault("logging.level", "debug")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "stdout")
	v.SetDefault("environment", "development")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}
	cfg.Environment = v.GetString("environment")
	return &cfg, nil
}
