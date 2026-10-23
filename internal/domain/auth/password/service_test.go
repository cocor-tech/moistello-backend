package password

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashPassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
	}{
		{
			name:     "alphanumeric password",
			password: "P@ssw0rd123456",
		},
		{
			name:     "password with spaces and unicode",
			password: "correct horse battery staple 🔐",
		},
		{
			name:     "empty password",
			password: "",
		},
		{
			name:     "long password",
			password: strings.Repeat("A-very-long-password!", 10),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := HashPassword(tt.password)
			require.NoError(t, err)
			assert.NotEmpty(t, hash)

			// Check format: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
			parts := strings.Split(hash, "$")
			require.Len(t, parts, 6, "hash must have 6 parts separated by $")
			assert.Equal(t, "", parts[0])
			assert.Equal(t, "argon2id", parts[1])
			assert.Equal(t, "v=19", parts[2])
			assert.Equal(t, "m=65536,t=3,p=4", parts[3])

			salt, err := base64Decode(parts[4])
			require.NoError(t, err)
			assert.Len(t, salt, 16, "salt must be 16 bytes")

			rawHash, err := base64Decode(parts[5])
			require.NoError(t, err)
			assert.Len(t, rawHash, 32, "hash must be 32 bytes")
		})
	}
}

func TestHashPassword_RandomSalts(t *testing.T) {
	pw := "samePassword"
	hash1, err := HashPassword(pw)
	require.NoError(t, err)

	hash2, err := HashPassword(pw)
	require.NoError(t, err)

	assert.NotEqual(t, hash1, hash2, "hashes for the same password must differ due to random salt")
}

func TestVerifyPassword(t *testing.T) {
	validPW := "secretPassphrase123!"
	validHash, err := HashPassword(validPW)
	require.NoError(t, err)

	tests := []struct {
		name        string
		password    string
		encodedHash string
		want        bool
	}{
		{
			name:        "valid password and matching hash",
			password:    validPW,
			encodedHash: validHash,
			want:        true,
		},
		{
			name:        "wrong password with valid hash",
			password:    "wrongPassword",
			encodedHash: validHash,
			want:        false,
		},
		{
			name:        "empty password against valid hash",
			password:    "",
			encodedHash: validHash,
			want:        false,
		},
		{
			name:        "empty string hash",
			password:    validPW,
			encodedHash: "",
			want:        false,
		},
		{
			name:        "invalid format without dollar signs",
			password:    validPW,
			encodedHash: "notavalidhash",
			want:        false,
		},
		{
			name:        "insufficient parts in hash",
			password:    validPW,
			encodedHash: "$argon2id$v=19$m=65536,t=3,p=4$salt",
			want:        false,
		},
		{
			name:        "too many parts in hash",
			password:    validPW,
			encodedHash: "$argon2id$v=19$m=65536,t=3,p=4$salt$hash$extra",
			want:        false,
		},
		{
			name:        "unsupported algorithm",
			password:    validPW,
			encodedHash: "$bcrypt$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
			want:        false,
		},
		{
			name:        "invalid base64 salt",
			password:    validPW,
			encodedHash: "$argon2id$v=19$m=65536,t=3,p=4$!invalidBase64!$c2FsdA",
			want:        false,
		},
		{
			name:        "invalid base64 hash",
			password:    validPW,
			encodedHash: "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$!invalidBase64!",
			want:        false,
		},
		{
			name:        "tampered hash payload",
			password:    validPW,
			encodedHash: validHash[:len(validHash)-4] + "AAAA",
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VerifyPassword(tt.password, tt.encodedHash)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVerifyPassword_EmptyPassword(t *testing.T) {
	emptyHash, err := HashPassword("")
	require.NoError(t, err)

	assert.True(t, VerifyPassword("", emptyHash), "empty password should verify against its own hash")
	assert.False(t, VerifyPassword("not-empty", emptyHash), "non-empty password should not verify against empty password hash")
}

func TestBase64EncodeAndDecode(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "empty slice",
			data: []byte{},
		},
		{
			name: "single byte (len % 4 == 2 encoded)",
			data: []byte{0x01},
		},
		{
			name: "two bytes (len % 4 == 3 encoded)",
			data: []byte{0x01, 0x02},
		},
		{
			name: "three bytes (len % 4 == 0 encoded)",
			data: []byte{0x01, 0x02, 0x03},
		},
		{
			name: "four bytes",
			data: []byte{0x01, 0x02, 0x03, 0x04},
		},
		{
			name: "16 bytes (salt size)",
			data: []byte("0123456789abcdef"),
		},
		{
			name: "32 bytes (key size)",
			data: []byte("0123456789abcdef0123456789abcdef"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := base64Encode(tt.data)
			assert.False(t, strings.HasSuffix(encoded, "="), "base64Encode should trim '=' padding")

			decoded, err := base64Decode(encoded)
			require.NoError(t, err)
			assert.Equal(t, tt.data, decoded)
		})
	}

	t.Run("invalid base64 characters return error", func(t *testing.T) {
		_, err := base64Decode("!!!not-valid-base64???")
		assert.Error(t, err)
	})
}
