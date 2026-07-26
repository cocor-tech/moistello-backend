package yellowcard_test

import (
	"testing"

	"clients/yellowcard"
)

func TestYellowCardHMACSignature(t *testing.T) {
	t.Setenv("YELLOW_CARD_API_KEY", "test_key_123")
	t.Setenv("YELLOW_CARD_API_SECRET", "super_secret_key_456")

	client, err := yellowcard.NewClient("https://api.yellowcard.io")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	t.Run("generates deterministic HMAC signature", func(t *testing.T) {
		timestamp := "1600000000"
		method := "POST"
		path := "/v1/fiat/offramp"
		payload := []byte(`{"amount":100,"currency":"NGN"}`)

		sig := client.GenerateHMACSignature(timestamp, method, path, payload)
		if sig == "" {
			t.Fatal("expected non-empty HMAC signature string")
		}

		// Re-run with identical parameters to verify determinism
		sig2 := client.GenerateHMACSignature(timestamp, method, path, payload)
		if sig != sig2 {
			t.Fatalf("expected identical signatures, got %s and %s", sig, sig2)
		}
	})
}