package auth_test

import (
	"testing"

	"auth"
)

func TestArgon2ConfigurableHasher(t *testing.T) {
	t.Run("uses custom low-memory configuration for test environments", func(t *testing.T) {
		lowMemCfg := auth.Argon2Config{
			Time:    1,
			Memory:  16 * 1024, // 16MB for fast CI execution
			Threads: 1,
			KeyLen:  32,
			SaltLen: 16,
		}

		hasher := auth.NewPasswordHasher(lowMemCfg)
		password := "SuperSecretPassword123!"

		encoded, err := hasher.HashPassword(password)
		if err != nil {
			t.Fatalf("failed to hash password: %v", err)
		}

		match, err := hasher.VerifyPassword(password, encoded)
		if err != nil || !match {
			t.Fatalf("expected valid password verification, got match=%v, err=%v", match, err)
		}

		matchWrong, _ := hasher.VerifyPassword("WrongPassword!", encoded)
		if matchWrong {
			t.Fatal("expected password mismatch for incorrect input")
		}
	})
}

package auth_test

import (
	"testing"

	"auth"
)

func TestArgon2ConfigurableHasher(t *testing.T) {
	t.Run("hashes and verifies password using low-memory configuration", func(t *testing.T) {
		lowMemCfg := auth.Argon2Config{
			Time:    1,
			Memory:  16 * 1024, // 16MB for low-memory deployments & fast CI execution
			Threads: 1,
			KeyLen:  32,
			SaltLen: 16,
		}

		hasher := auth.NewPasswordHasher(lowMemCfg)
		password := "SecureDevPassword123!"

		encoded, err := hasher.HashPassword(password)
		if err != nil {
			t.Fatalf("failed to hash password: %v", err)
		}

		match, err := hasher.VerifyPassword(password, encoded)
		if err != nil || !match {
			t.Fatalf("expected valid password verification, got match=%v, err=%v", match, err)
		}
	})
}