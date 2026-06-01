package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
)

type VerificationService struct {
	store   VerificationStore
	emailer EmailSender
	limiter RateLimiter
	config  VerificationConfig
}

func NewVerificationService(store VerificationStore, emailer EmailSender, limiter RateLimiter, config VerificationConfig) *VerificationService {
	if config.CodeLength == 0 {
		config.CodeLength = 6
	}
	if config.CodeExpiry == 0 {
		config.CodeExpiry = 10 * time.Minute
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = 5
	}
	if config.MaxSendsPerEmail == 0 {
		config.MaxSendsPerEmail = 3
	}
	if config.ResendCooldown == 0 {
		config.ResendCooldown = 60 * time.Second
	}
	return &VerificationService{
		store:   store,
		emailer: emailer,
		limiter: limiter,
		config:  config,
	}
}

func (s *VerificationService) SendCode(ctx context.Context, email string) (*SendCodeResponse, error) {
	allowed, retryAfter, err := s.limiter.Check(ctx, "email_send:"+email, s.config.MaxSendsPerEmail, 10*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("rate limit check failed: %w", err)
	}
	if !allowed {
		return nil, &RateLimitError{
			Message:    fmt.Sprintf("Too many requests. Try again in %d seconds.", int(retryAfter.Seconds())),
			RetryAfter: retryAfter,
		}
	}

	code, err := generateCode(s.config.CodeLength)
	if err != nil {
		return nil, fmt.Errorf("code generation failed: %w", err)
	}

	codeHash := hashVerificationCode(code)

	verification := &VerificationCode{
		ID:          uuid.New().String(),
		Email:       email,
		CodeHash:    codeHash,
		ExpiresAt:   time.Now().UTC().Add(s.config.CodeExpiry),
		Attempts:    0,
		MaxAttempts: s.config.MaxAttempts,
		CreatedAt:   time.Now().UTC(),
	}

	if err := s.store.Save(ctx, verification); err != nil {
		return nil, fmt.Errorf("failed to save verification: %w", err)
	}

	body := fmt.Sprintf("Your Moistello verification code is: %s\n\nThis code expires in 10 minutes.\n\nIf you did not request this code, please ignore this email.", code)
	if err := s.emailer.Send(ctx, email, "Your Moistello verification code", body); err != nil {
		_ = s.store.Delete(ctx, verification.ID)
		return nil, fmt.Errorf("failed to send email: %w", err)
	}

	_ = s.limiter.Increment(ctx, "email_send:"+email, 10*time.Minute)

	return &SendCodeResponse{
		VerificationID:    verification.ID,
		ExpiresAt:         verification.ExpiresAt.UnixMilli(),
		RemainingAttempts: s.config.MaxSendsPerEmail,
	}, nil
}

func (s *VerificationService) VerifyCode(ctx context.Context, verificationID, code string) (*VerifyCodeResponse, error) {
	verification, err := s.store.FindByID(ctx, verificationID)
	if err != nil {
		return nil, &VerifyError{Message: "Verification not found. Please request a new code.", StatusCode: 404}
	}

	if time.Now().UTC().After(verification.ExpiresAt) {
		_ = s.store.Delete(ctx, verificationID)
		return nil, &VerifyError{Message: "Code has expired. Please request a new one.", StatusCode: 410}
	}

	if verification.Attempts >= verification.MaxAttempts {
		_ = s.store.Delete(ctx, verificationID)
		return nil, &VerifyError{Message: "Too many incorrect attempts. Please request a new code.", StatusCode: 429}
	}

	codeHash := hashVerificationCode(code)
	if subtle.ConstantTimeCompare([]byte(codeHash), []byte(verification.CodeHash)) != 1 {
		verification.Attempts++
		_ = s.store.Save(ctx, verification)

		remaining := verification.MaxAttempts - verification.Attempts
		if remaining <= 0 {
			_ = s.store.Delete(ctx, verificationID)
			return nil, &VerifyError{Message: "No attempts remaining. Please request a new code.", StatusCode: 429, Remaining: 0}
		}
		return nil, &VerifyError{
			Message:   fmt.Sprintf("Invalid code. %d attempt(s) remaining.", remaining),
			StatusCode: 400,
			Remaining: remaining,
		}
	}

	if err := s.store.MarkEmailVerified(ctx, verification.Email); err != nil {
		return nil, fmt.Errorf("failed to mark email verified: %w", err)
	}

	_ = s.store.Delete(ctx, verificationID)

	return &VerifyCodeResponse{Verified: true}, nil
}

func (s *VerificationService) ResendCode(ctx context.Context, verificationID string) (*SendCodeResponse, error) {
	verification, err := s.store.FindByID(ctx, verificationID)
	if err != nil {
		return nil, &VerifyError{Message: "Verification not found.", StatusCode: 404}
	}

	allowed, retryAfter, err := s.limiter.Check(ctx, "email_resend:"+verification.Email, 1, s.config.ResendCooldown)
	if err != nil {
		return nil, fmt.Errorf("rate limit check failed: %w", err)
	}
	if !allowed {
		return nil, &RateLimitError{
			Message:    fmt.Sprintf("Please wait %d seconds before requesting a new code.", int(retryAfter.Seconds())),
			RetryAfter: retryAfter,
		}
	}

	if time.Now().UTC().After(verification.ExpiresAt) {
		_ = s.store.Delete(ctx, verificationID)
		return s.SendCode(ctx, verification.Email)
	}

	code, err := generateCode(s.config.CodeLength)
	if err != nil {
		return nil, fmt.Errorf("code generation failed: %w", err)
	}

	codeHash := hashVerificationCode(code)
	verification.CodeHash = codeHash
	verification.Attempts = 0
	verification.ExpiresAt = time.Now().UTC().Add(s.config.CodeExpiry)

	if err := s.store.Save(ctx, verification); err != nil {
		return nil, fmt.Errorf("failed to update verification: %w", err)
	}

	body := fmt.Sprintf("Your new Moistello verification code is: %s\n\nThis code expires in 10 minutes.", code)
	if err := s.emailer.Send(ctx, verification.Email, "Your new Moistello verification code", body); err != nil {
		return nil, fmt.Errorf("failed to send email: %w", err)
	}

	_ = s.limiter.Increment(ctx, "email_resend:"+verification.Email, s.config.ResendCooldown)

	return &SendCodeResponse{
		VerificationID:    verification.ID,
		ExpiresAt:         verification.ExpiresAt.UnixMilli(),
		RemainingAttempts: s.config.MaxAttempts,
	}, nil
}

func (s *VerificationService) CheckEmailVerified(ctx context.Context, email, verificationID string) error {
	verified, err := s.store.IsEmailVerified(ctx, email)
	if err != nil {
		return fmt.Errorf("failed to check email verification: %w", err)
	}
	if !verified {
		return &VerifyError{Message: "Email not verified. Please complete email verification first.", StatusCode: 403}
	}

	verification, err := s.store.FindByID(ctx, verificationID)
	if err != nil {
		return &VerifyError{Message: "Verification not found.", StatusCode: 404}
	}
	if verification.Email != email {
		return &VerifyError{Message: "Verification ID does not match the provided email.", StatusCode: 403}
	}

	return nil
}

func generateCode(digits int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("failed to generate random code: %w", err)
	}
	format := fmt.Sprintf("%%0%dd", digits)
	return fmt.Sprintf(format, n), nil
}

func hashVerificationCode(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

type RateLimitError struct {
	Message    string
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string { return e.Message }

type VerifyError struct {
	Message    string
	StatusCode int
	Remaining  int
}

func (e *VerifyError) Error() string { return e.Message }
