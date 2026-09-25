package crypto

import (
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// Conservative Argon2id defaults used when configured parameters are not
// positive. They mirror the values shipped in the API server configuration.
const (
	DefaultArgon2Time    = 1
	DefaultArgon2Memory  = 64 * 1024
	DefaultArgon2Threads = 4

	derivedKeyLen = 32
)

// DeriveWalletSeed deterministically derives a hex-encoded 32-byte seed from
// an email address and the server pepper using Argon2id. Using the email in
// the salt guarantees the same email always maps to the same wallet while the
// pepper keeps the derivation server-side secret. Non-positive Argon2id
// parameters fall back to the conservative defaults.
func DeriveWalletSeed(email, pepper string, argon2Time, argon2Memory, argon2Threads int) (string, error) {
	if pepper == "" {
		return "", fmt.Errorf("wallet pepper is not configured")
	}

	if argon2Time <= 0 {
		argon2Time = DefaultArgon2Time
	}
	if argon2Memory <= 0 {
		argon2Memory = DefaultArgon2Memory
	}
	if argon2Threads <= 0 {
		argon2Threads = DefaultArgon2Threads
	}

	salt := []byte(pepper + email)
	key := argon2.IDKey([]byte(email), salt, uint32(argon2Time), uint32(argon2Memory), uint8(argon2Threads), derivedKeyLen)
	return hex.EncodeToString(key), nil
}
