package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

type Argon2Config struct {
	Time    uint32 `mapstructure:"time" yaml:"time"`       // Number of iterations
	Memory  uint32 `mapstructure:"memory" yaml:"memory"`   // Memory in KiB
	Threads uint8  `mapstructure:"threads" yaml:"threads"` // Parallelism degree
	KeyLen  uint32 `mapstructure:"key_len" yaml:"key_len"` // Output hash length
	SaltLen uint32 `mapstructure:"salt_len" yaml:"salt_len"`
}

// DefaultArgon2Config provides sensible production defaults (64MB memory, 3 iterations, 4 threads).
func DefaultArgon2Config() Argon2Config {
	return Argon2Config{
		Time:    3,
		Memory:  64 * 1024,
		Threads: 4,
		KeyLen:  32,
		SaltLen: 16,
	}
}

type PasswordHasher struct {
	cfg Argon2Config
}

func NewPasswordHasher(cfg Argon2Config) *PasswordHasher {
	// Fall back to defaults if unconfigured/zero-valued
	if cfg.Time == 0 {
		cfg.Time = 3
	}
	if cfg.Memory == 0 {
		cfg.Memory = 64 * 1024
	}
	if cfg.Threads == 0 {
		cfg.Threads = 4
	}
	if cfg.KeyLen == 0 {
		cfg.KeyLen = 32
	}
	if cfg.SaltLen == 0 {
		cfg.SaltLen = 16
	}

	return &PasswordHasher{cfg: cfg}
}

// HashPassword generates a secure Argon2id hash using configured parameters.
func (h *PasswordHasher) HashPassword(password string) (string, error) {
	salt := make([]byte, h.cfg.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate random salt: %w", err)
	}

	hash := argon2.IDKey(
		[]byte(password),
		salt,
		h.cfg.Time,
		h.cfg.Memory,
		h.cfg.Threads,
		h.cfg.KeyLen,
	)

	// Format: $argon2id$v=19$m=65536,t=3,p=4$<salt_hex>$<hash_hex>
	encoded := fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		h.cfg.Memory,
		h.cfg.Time,
		h.cfg.Threads,
		hex.EncodeToString(salt),
		hex.EncodeToString(hash),
	)

	return encoded, nil
}

// VerifyPassword verifies a raw password against an encoded Argon2id hash.
func (h *PasswordHasher) VerifyPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("invalid Argon2id hash format")
	}

	var memory, time uint32
	var threads uint8
	_, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads)
	if err != nil {
		return false, fmt.Errorf("failed to parse hash parameters: %w", err)
	}

	salt, err := hex.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("failed to decode salt: %w", err)
	}

	expectedHash, err := hex.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("failed to decode hash: %w", err)
	}

	computedHash := argon2.IDKey(
		[]byte(password),
		salt,
		time,
		memory,
		threads,
		uint32(len(expectedHash)),
	)

	return subtle.ConstantTimeCompare(expectedHash, computedHash) == 1, nil
}

package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

type Argon2Config struct {
	Time    uint32 `mapstructure:"time" yaml:"time"`       // Number of iterations
	Memory  uint32 `mapstructure:"memory" yaml:"memory"`   // Memory in KiB
	Threads uint8  `mapstructure:"threads" yaml:"threads"` // Degree of parallelism
	KeyLen  uint32 `mapstructure:"key_len" yaml:"key_len"` // Output hash length
	SaltLen uint32 `mapstructure:"salt_len" yaml:"salt_len"`
}

// DefaultArgon2Config provides sensible production defaults (64MB memory, 3 iterations, 4 threads).
func DefaultArgon2Config() Argon2Config {
	return Argon2Config{
		Time:    3,
		Memory:  64 * 1024,
		Threads: 4,
		KeyLen:  32,
		SaltLen: 16,
	}
}

type PasswordHasher struct {
	cfg Argon2Config
}

func NewPasswordHasher(cfg Argon2Config) *PasswordHasher {
	// Fall back to defaults if unconfigured/zero-valued
	if cfg.Time == 0 {
		cfg.Time = 3
	}
	if cfg.Memory == 0 {
		cfg.Memory = 64 * 1024
	}
	if cfg.Threads == 0 {
		cfg.Threads = 4
	}
	if cfg.KeyLen == 0 {
		cfg.KeyLen = 32
	}
	if cfg.SaltLen == 0 {
		cfg.SaltLen = 16
	}

	return &PasswordHasher{cfg: cfg}
}

// HashPassword generates a secure Argon2id hash using environment-configured parameters.
func (h *PasswordHasher) HashPassword(password string) (string, error) {
	salt := make([]byte, h.cfg.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate random salt: %w", err)
	}

	hash := argon2.IDKey(
		[]byte(password),
		salt,
		h.cfg.Time,
		h.cfg.Memory,
		h.cfg.Threads,
		h.cfg.KeyLen,
	)

	// Format: $argon2id$v=19$m=65536,t=3,p=4$<salt_hex>$<hash_hex>
	encoded := fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		h.cfg.Memory,
		h.cfg.Time,
		h.cfg.Threads,
		hex.EncodeToString(salt),
		hex.EncodeToString(hash),
	)

	return encoded, nil
}

// VerifyPassword verifies a raw password against an encoded Argon2id hash string.
func (h *PasswordHasher) VerifyPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("invalid Argon2id hash format")
	}

	var memory, time uint32
	var threads uint8
	_, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads)
	if err != nil {
		return false, fmt.Errorf("failed to parse hash parameters: %w", err)
	}

	salt, err := hex.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("failed to decode salt: %w", err)
	}

	expectedHash, err := hex.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("failed to decode hash: %w", err)
	}

	computedHash := argon2.IDKey(
		[]byte(password),
		salt,
		time,
		memory,
		threads,
		uint32(len(expectedHash)),
	)

	return subtle.ConstantTimeCompare(expectedHash, computedHash) == 1, nil
}