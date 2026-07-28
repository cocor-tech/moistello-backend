package utils_test

import (
	"encoding/hex"
	"testing"

	"src/utils"
)

func TestFieldEncryption(t *testing.T) {
	// Setup 32-byte (256-bit) hex key for testing
	testKey := hex.EncodeToString([]byte("12345678901234567890123456789012"))

	plainEmail := "user@example.com"

	t.Run("encrypts and decrypts email cleanly", func(t *testing.T) {
		cipherHex, err := utils.EncryptField(testKey, plainEmail)
		if err != nil {
			t.Fatalf("encryption failed: %v", err)
		}

		if cipherHex == plainEmail {
			t.Fatal("expected ciphertext to differ from plaintext email")
		}

		decrypted, err := utils.DecryptField(testKey, cipherHex)
		if err != nil {
			t.Fatalf("decryption failed: %v", err)
		}

		if decrypted != plainEmail {
			t.Fatalf("expected %s, got %s", plainEmail, decrypted)
		}
	})

	t.Run("fails when ENCRYPTION_KEY is unconfigured", func(t *testing.T) {
		_, err := utils.EncryptField("", plainEmail)
		if err == nil {
			t.Fatal("expected error when ENCRYPTION_KEY is missing, got nil")
		}
	})
}
