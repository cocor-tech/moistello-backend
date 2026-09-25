package totp

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"image/png"
	"math/big"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	// Issuer is the name shown in authenticator apps.
	Issuer = "Moistello"
	// BackupCodeCount is how many single-use recovery codes an enrollment gets.
	BackupCodeCount = 10
	// DefaultQRSize is the pixel width and height of provisioning QR images.
	DefaultQRSize = 256

	period    = 30
	digits    = otp.DigitsSix
	algorithm = otp.AlgorithmSHA1
)

// Service handles TOTP (Time-based One-Time Password) operations
// using the standard TOTP algorithm (RFC 6238).
type Service struct{}

func NewService() *Service {
	return &Service{}
}

// GenerateSecret creates a new TOTP secret and returns it along with
// the standard otpauth:// URI for QR code generation.
// accountName identifies the account in the authenticator app.
func (s *Service) GenerateSecret(accountName string) (secret string, uri string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      Issuer,
		AccountName: accountName,
		Period:      period,
		SecretSize:  20,
		Digits:      digits,
		Algorithm:   algorithm,
	})
	if err != nil {
		return "", "", fmt.Errorf("generating TOTP secret: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// ProvisioningQR renders an otpauth:// URI as a PNG QR code of size×size
// pixels so clients can show it during enrollment without a second library.
func (s *Service) ProvisioningQR(uri string, size int) ([]byte, error) {
	if size <= 0 {
		size = DefaultQRSize
	}
	key, err := otp.NewKeyFromURL(uri)
	if err != nil {
		return nil, fmt.Errorf("parsing provisioning URI: %w", err)
	}
	if key.Type() != "totp" || key.Secret() == "" {
		return nil, fmt.Errorf("parsing provisioning URI: not an otpauth://totp URI with a secret")
	}
	img, err := key.Image(size, size)
	if err != nil {
		return nil, fmt.Errorf("rendering QR code: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encoding QR code: %w", err)
	}
	return buf.Bytes(), nil
}

// ValidateCode checks if the provided TOTP code is valid for the given secret.
// It allows a 30-second skew (one period before and after) to account for
// clock drift between the server and the authenticator app.
func (s *Service) ValidateCode(secret, code string) bool {
	return s.ValidateCodeAt(secret, code, time.Now().UTC())
}

// ValidateCodeAt is ValidateCode evaluated at an explicit instant.
func (s *Service) ValidateCodeAt(secret, code string, at time.Time) bool {
	valid, err := totp.ValidateCustom(
		strings.TrimSpace(code),
		secret,
		at,
		totp.ValidateOpts{
			Period:    period,
			Skew:      1,
			Digits:    digits,
			Algorithm: algorithm,
		},
	)
	return err == nil && valid
}

// BackupCode represents a single backup/recovery code.
type BackupCode struct {
	Plain string // shown to user once
	Hash  string // stored as SHA-256
}

// GenerateBackupCodes creates BackupCodeCount backup codes in the form
// XXXX-XXXX-XXXX drawn from an alphabet without look-alike characters.
// Returns both the plain versions (to show the user) and hashed versions (to store).
func (s *Service) GenerateBackupCodes() ([]BackupCode, error) {
	codes := make([]BackupCode, BackupCodeCount)
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no I,O,0,1 to avoid confusion
	for i := range codes {
		plain := make([]byte, 14)
		for j := range plain {
			if j == 4 || j == 9 {
				plain[j] = '-'
				continue
			}
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
			if err != nil {
				return nil, fmt.Errorf("generating backup code: %w", err)
			}
			plain[j] = charset[n.Int64()]
		}
		code := string(plain)
		codes[i] = BackupCode{Plain: code, Hash: hashBackupCode(code)}
	}
	return codes, nil
}

// NormalizeBackupCode makes user input comparable with generated codes:
// whitespace is removed, letters are upper-cased and the separators are
// restored so "abcd efgh jklm" and "ABCD-EFGH-JKLM" hash identically.
func NormalizeBackupCode(code string) string {
	raw := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(strings.TrimSpace(code)))
	if len(raw) != 12 {
		return strings.ToUpper(strings.TrimSpace(code))
	}
	return raw[0:4] + "-" + raw[4:8] + "-" + raw[8:12]
}

// ValidateBackupCode checks a plain backup code against the stored hashed
// codes. Returns the updated list of hashed codes (with the used code removed)
// and true if the code was valid, or nil and false if invalid. A code can
// therefore be redeemed exactly once. Comparison is constant-time.
func (s *Service) ValidateBackupCode(plainCode string, hashedCodes []string) ([]string, bool) {
	want := []byte(hashBackupCode(NormalizeBackupCode(plainCode)))
	match := -1
	for i, stored := range hashedCodes {
		if subtle.ConstantTimeCompare([]byte(stored), want) == 1 && match == -1 {
			match = i
		}
	}
	if match == -1 {
		return nil, false
	}
	remaining := make([]string, 0, len(hashedCodes)-1)
	remaining = append(remaining, hashedCodes[:match]...)
	remaining = append(remaining, hashedCodes[match+1:]...)
	return remaining, true
}

// HashBackupCodes returns only the hashed versions of backup codes for storage.
func (s *Service) HashBackupCodes(codes []BackupCode) []string {
	hashed := make([]string, len(codes))
	for i, c := range codes {
		hashed[i] = c.Hash
	}
	return hashed
}

func hashBackupCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}
