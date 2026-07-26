// config/config.go
type Config struct {
    Stellar struct {
        HorizonURL      string `yaml:"horizon_url"`
        MasterSecretKey string `env:"STELLAR_MASTER_SECRET_KEY" required:"true"`
    } `yaml:"stellar"`
}

func LoadConfig() (*Config, error) {
    var cfg Config
    // 1. Read YAML file for non-sensitive defaults
    // 2. Bind/Override with Environment Variables
    cfg.Stellar.MasterSecretKey = os.Getenv("STELLAR_MASTER_SECRET_KEY")
    if cfg.Stellar.MasterSecretKey == "" {
        return nil, errors.New("CRITICAL: STELLAR_MASTER_SECRET_KEY environment variable is not set")
    }
    return &cfg, nil
}