package wallet

import (
	"errors"
	"net/http"
	"time"

	"github.com/stellar/go/clients/horizonclient"
)

type Config struct {
	HorizonURL string `mapstructure:"horizon_url" yaml:"horizon_url"`
	UseTestnet bool   `mapstructure:"use_testnet" yaml:"use_testnet"`
}

type WalletService struct {
	horizonClient horizonclient.ClientInterface
}

// NewHorizonService initializes a Horizon-backed wallet service from the provided config.
func NewHorizonService(cfg Config) (*WalletService, error) {
	if cfg.HorizonURL == "" {
		return nil, errors.New("invalid configuration: horizon_url must not be empty")
	}

	// Construct dynamic client targeting configured network URL
	client := &horizonclient.Client{
		HorizonURL: cfg.HorizonURL,
		HTTP:       &http.Client{Timeout: 15 * time.Second},
	}

	return &WalletService{
		horizonClient: client,
	}, nil
}
