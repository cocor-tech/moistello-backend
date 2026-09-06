package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
	argonKeyLen  = 32
)

// HashPassword hashes a plaintext password using Argon2id with a random salt.
// Returns the encoded hash in the format: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	buf := &strings.Builder{}
	buf.WriteString("$argon2id$v=19$m=65536,t=3,p=4$")
	buf.WriteString(base64Encode(salt))
	buf.WriteByte('$')
	buf.WriteString(base64Encode(hash))
	return buf.String(), nil
}

// VerifyPassword checks a plaintext password against an Argon2id encoded hash.
func VerifyPassword(password, encodedHash string) bool {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	salt, err := base64Decode(parts[4])
	if err != nil {
		return false
	}

	expected, err := base64Decode(parts[5])
	if err != nil {
		return false
	}

	computed := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return subtle.ConstantTimeCompare(computed, expected) == 1
}

func base64Encode(data []byte) string {
	return strings.TrimRight(base64.StdEncoding.EncodeToString(data), "=")
}

func base64Decode(s string) ([]byte, error) {
	// Add padding
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.StdEncoding.DecodeString(s)
}
