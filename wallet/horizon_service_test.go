package wallet_test

import (
	"testing"

	"wallet"
)

func TestNewHorizonService_Config(t *testing.T) {
	t.Run("successfully configures custom horizon URL", func(t *testing.T) {
		cfg := wallet.Config{
			HorizonURL: "https://horizon.stellar.org", // Mainnet target
		}

		svc, err := wallet.NewHorizonService(cfg)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if svc == nil {
			t.Fatal("expected service instance to be non-nil")
		}
	})

	t.Run("fails when horizon URL is unconfigured", func(t *testing.T) {
		cfg := wallet.Config{HorizonURL: ""}

		_, err := wallet.NewHorizonService(cfg)
		if err == nil {
			t.Fatal("expected error for empty horizon_url, got nil")
		}
	})
}
