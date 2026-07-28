package verification

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const pendingRegistrationTTL = 15 * time.Minute

// PendingRegistration holds user data that hasn't been verified yet.
// Stored in Redis until email OTP verification completes.
type PendingRegistration struct {
	PasswordHash string `json:"passwordHash"`
	WalletAddr   string `json:"walletAddr"`
	Email        string `json:"email"`
	DisplayName  string `json:"displayName,omitempty"`
	Language     string `json:"language,omitempty"`
}

const (
	otpTTL      = 5 * time.Minute
	resendTTL   = 60 * time.Second
	maxAttempts = 5
	recoveryTTL = 15 * time.Minute
)

// Service handles email verification OTP codes stored in Redis.
type Service struct {
	rdb              *redis.Client
	sendOTP          func(email, code string) error
	sendRecoveryCode func(email, code string) error
}

func NewService(rdb *redis.Client) *Service {
	return &Service{rdb: rdb}
}

// WithEmailSender attaches email delivery funcs so OTP/recovery codes are
// delivered directly within the Service layer — plaintext codes never cross
// the package boundary.
func (s *Service) WithEmailSender(sendOTP, sendRecoveryCode func(email, code string) error) {
	s.sendOTP = sendOTP
	s.sendRecoveryCode = sendRecoveryCode
}

// SendOTP generates a 6-digit code, stores it (hashed) in Redis, and delivers
// it directly via the injected email sender (if configured). On success the
// plaintext code is never returned.
func (s *Service) SendOTP(ctx context.Context, email string) error {
	// Rate limit: check if a code was sent recently
	resendKey := fmt.Sprintf("otp:resend:%s", email)
	exists, err := s.rdb.Exists(ctx, resendKey).Result()
	if err != nil {
		return fmt.Errorf("checking resend rate limit: %w", err)
	}
	if exists > 0 {
		return fmt.Errorf("please wait before requesting another code")
	}

	// Generate a 6-digit code
	code, err := generateNumericCode(6)
	if err != nil {
		return fmt.Errorf("generating OTP code: %w", err)
	}

	// Store hashed code in Redis
	codeHash := sha256.Sum256([]byte(code))
	hashedCode := hex.EncodeToString(codeHash[:])
	otpKey := fmt.Sprintf("otp:code:%s", email)
	otpValue := fmt.Sprintf("%s:0", hashedCode) // hash:attempts
	if err := s.rdb.Set(ctx, otpKey, otpValue, otpTTL).Err(); err != nil {
		return fmt.Errorf("storing OTP: %w", err)
	}

	// Set resend rate limit
	s.rdb.Set(ctx, resendKey, "1", resendTTL)

	if s.sendOTP != nil {
		if err := s.sendOTP(email, code); err != nil {
			return fmt.Errorf("sending OTP email: %w", err)
		}
	}
	return nil
}

// VerifyOTP checks a 6-digit code against the stored hash.
// Returns true and marks email as verified, or false if invalid/expired.
func (s *Service) VerifyOTP(ctx context.Context, email, code string) (bool, error) {
	otpKey := fmt.Sprintf("otp:code:%s", email)
	stored, err := s.rdb.Get(ctx, otpKey).Result()
	if err == redis.Nil {
		return false, fmt.Errorf("OTP code expired or not found")
	}
	if err != nil {
		return false, fmt.Errorf("reading OTP: %w", err)
	}

	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		s.rdb.Del(ctx, otpKey)
		return false, fmt.Errorf("invalid OTP format")
	}

	attempts, _ := strconv.Atoi(parts[1])
	if attempts >= maxAttempts {
		s.rdb.Del(ctx, otpKey)
		return false, fmt.Errorf("too many incorrect attempts. request a new code")
	}

	codeHash := sha256.Sum256([]byte(code))
	hashedCode := hex.EncodeToString(codeHash[:])

	if hashedCode == parts[0] {
		s.rdb.Del(ctx, otpKey)
		return true, nil
	}

	// Increment attempts
	newValue := fmt.Sprintf("%s:%d", parts[0], attempts+1)
	s.rdb.Set(ctx, otpKey, newValue, otpTTL)
	return false, nil
}

// SendRecoveryCode generates a one-time recovery code, stores it (hashed) in
// Redis, and delivers it directly via the injected sender. The plaintext code
// is never returned.
func (s *Service) SendRecoveryCode(ctx context.Context, email string) error {
	code, err := generateNumericCode(8)
	if err != nil {
		return err
	}

	codeHash := sha256.Sum256([]byte(code))
	recoveryKey := fmt.Sprintf("recovery:code:%s", email)
	if err := s.rdb.Set(ctx, recoveryKey, hex.EncodeToString(codeHash[:]), recoveryTTL).Err(); err != nil {
		return fmt.Errorf("storing recovery code: %w", err)
	}

	if s.sendRecoveryCode != nil {
		if err := s.sendRecoveryCode(email, code); err != nil {
			return fmt.Errorf("sending recovery code email: %w", err)
		}
	}
	return nil
}

// VerifyRecoveryCode checks an 8-digit recovery code.
func (s *Service) VerifyRecoveryCode(ctx context.Context, email, code string) (bool, error) {
	recoveryKey := fmt.Sprintf("recovery:code:%s", email)
	stored, err := s.rdb.Get(ctx, recoveryKey).Result()
	if err == redis.Nil {
		return false, fmt.Errorf("recovery code expired or not found")
	}
	if err != nil {
		return false, fmt.Errorf("reading recovery code: %w", err)
	}

	codeHash := sha256.Sum256([]byte(code))
	if hex.EncodeToString(codeHash[:]) == stored {
		s.rdb.Del(ctx, recoveryKey)
		return true, nil
	}
	return false, nil
}

// StorePendingRegistration saves user data in Redis before email verification.
// Data is automatically deleted after 15 minutes.
func (s *Service) StorePendingRegistration(ctx context.Context, email string, data *PendingRegistration) error {
	key := fmt.Sprintf("pending:register:%s", email)
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling pending registration: %w", err)
	}
	return s.rdb.Set(ctx, key, payload, pendingRegistrationTTL).Err()
}

// GetPendingRegistration retrieves pending registration data from Redis.
// Returns nil if no pending registration exists or it expired.
func (s *Service) GetPendingRegistration(ctx context.Context, email string) (*PendingRegistration, error) {
	key := fmt.Sprintf("pending:register:%s", email)
	data, err := s.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading pending registration: %w", err)
	}
	var pr PendingRegistration
	if err := json.Unmarshal(data, &pr); err != nil {
		return nil, fmt.Errorf("unmarshaling pending registration: %w", err)
	}
	return &pr, nil
}

// DeletePendingRegistration removes pending registration data from Redis.
func (s *Service) DeletePendingRegistration(ctx context.Context, email string) error {
	key := fmt.Sprintf("pending:register:%s", email)
	return s.rdb.Del(ctx, key).Err()
}

func generateNumericCode(length int) (string, error) {
	code := make([]byte, length)
	for i := range code {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", fmt.Errorf("generating random digit: %w", err)
		}
		code[i] = byte('0') + byte(n.Int64())
	}
	return string(code), nil
}
