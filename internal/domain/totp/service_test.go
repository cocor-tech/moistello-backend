package totp

import (
	"bytes"
	"image/png"
	"regexp"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateSecret_ProducesStandardProvisioningURI(t *testing.T) {
	svc := NewService()
	secret, uri, err := svc.GenerateSecret("jane@example.com")
	require.NoError(t, err)

	key, err := otp.NewKeyFromURL(uri)
	require.NoError(t, err, "authenticator apps must be able to parse the URI")
	assert.Equal(t, "totp", key.Type())
	assert.Equal(t, Issuer, key.Issuer())
	assert.Equal(t, "jane@example.com", key.AccountName())
	assert.Equal(t, secret, key.Secret())
	assert.Equal(t, otp.DigitsSix, key.Digits())
	assert.Equal(t, otp.AlgorithmSHA1, key.Algorithm())
	assert.Equal(t, uint64(30), key.Period())
	assert.Len(t, secret, 32, "20 random bytes base32-encode to 32 characters")
}

func TestValidateCode_AgainstReferenceImplementation(t *testing.T) {
	svc := NewService()
	secret, _, err := svc.GenerateSecret("acct")
	require.NoError(t, err)

	now := time.Date(2026, 9, 25, 12, 0, 15, 0, time.UTC)
	code, err := totp.GenerateCode(secret, now)
	require.NoError(t, err)

	assert.True(t, svc.ValidateCodeAt(secret, code, now))
	assert.True(t, svc.ValidateCodeAt(secret, " "+code+" ", now), "surrounding whitespace is tolerated")
	assert.True(t, svc.ValidateCodeAt(secret, code, now.Add(30*time.Second)), "one period of skew is allowed")
	assert.True(t, svc.ValidateCodeAt(secret, code, now.Add(-30*time.Second)))
	assert.False(t, svc.ValidateCodeAt(secret, code, now.Add(90*time.Second)), "codes expire after the skew window")
	assert.False(t, svc.ValidateCodeAt(secret, "000000", now.Add(time.Hour)))
	assert.False(t, svc.ValidateCodeAt(secret, "12345", now), "wrong length is rejected")
	assert.False(t, svc.ValidateCodeAt("not-base32!", code, now))

	other, _, err := svc.GenerateSecret("other")
	require.NoError(t, err)
	assert.False(t, svc.ValidateCodeAt(other, code, now), "codes are bound to their secret")
}

func TestProvisioningQR_IsDecodablePNG(t *testing.T) {
	svc := NewService()
	_, uri, err := svc.GenerateSecret("qr@example.com")
	require.NoError(t, err)

	data, err := svc.ProvisioningQR(uri, 200)
	require.NoError(t, err)
	img, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, 200, img.Bounds().Dx())
	assert.Equal(t, 200, img.Bounds().Dy())

	data, err = svc.ProvisioningQR(uri, 0)
	require.NoError(t, err)
	img, err = png.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, DefaultQRSize, img.Bounds().Dx())

	_, err = svc.ProvisioningQR("not a uri", 100)
	assert.Error(t, err)
}

func TestBackupCodes_FormatAndHashing(t *testing.T) {
	svc := NewService()
	codes, err := svc.GenerateBackupCodes()
	require.NoError(t, err)
	require.Len(t, codes, BackupCodeCount)

	format := regexp.MustCompile(`^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{4}-[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{4}-[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{4}$`)
	seen := map[string]bool{}
	for _, c := range codes {
		assert.Regexp(t, format, c.Plain)
		assert.Len(t, c.Hash, 64)
		assert.NotEqual(t, c.Plain, c.Hash)
		assert.False(t, seen[c.Plain], "codes must be unique")
		seen[c.Plain] = true
	}
	hashes := svc.HashBackupCodes(codes)
	assert.Equal(t, codes[3].Hash, hashes[3])
}

func TestBackupCodes_SingleUse(t *testing.T) {
	svc := NewService()
	codes, err := svc.GenerateBackupCodes()
	require.NoError(t, err)
	stored := svc.HashBackupCodes(codes)

	remaining, ok := svc.ValidateBackupCode(codes[2].Plain, stored)
	require.True(t, ok)
	assert.Len(t, remaining, BackupCodeCount-1)
	assert.NotContains(t, remaining, codes[2].Hash)

	_, ok = svc.ValidateBackupCode(codes[2].Plain, remaining)
	assert.False(t, ok, "a redeemed code must never work twice")

	_, ok = svc.ValidateBackupCode("ZZZZ-ZZZZ-ZZZZ", remaining)
	assert.False(t, ok)
	_, ok = svc.ValidateBackupCode("", remaining)
	assert.False(t, ok)
}

func TestBackupCodes_InputNormalization(t *testing.T) {
	svc := NewService()
	codes, err := svc.GenerateBackupCodes()
	require.NoError(t, err)
	stored := svc.HashBackupCodes(codes)

	plain := codes[0].Plain
	variants := []string{
		plain,
		" " + plain + " ",
		regexp.MustCompile(`-`).ReplaceAllString(plain, ""),
		regexp.MustCompile(`-`).ReplaceAllString(plain, " "),
		toLower(plain),
	}
	for _, v := range variants {
		_, ok := svc.ValidateBackupCode(v, stored)
		assert.True(t, ok, "variant %q should match", v)
	}
	assert.Equal(t, plain, NormalizeBackupCode(toLower(plain)))
	assert.Equal(t, "SHORT", NormalizeBackupCode(" short "))
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
