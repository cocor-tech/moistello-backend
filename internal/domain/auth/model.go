package auth

import (
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    uuid.UUID `json:"userId" db:"user_id"`
	TokenHash string    `json:"-" db:"token_hash"`
	ExpiresAt time.Time `json:"expiresAt" db:"expires_at"`
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
}

type Nonce struct {
	WalletAddress string    `json:"walletAddress"`
	Nonce         string    `json:"nonce"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

type JWTCustomClaims struct {
	UserID string `json:"sub"`
	Wallet string `json:"wallet"`
	Role   string `json:"role"`
}

type TokenPair struct {
	AccessToken  string `json:"token"`
	RefreshToken string `json:"refreshToken"`
}

type VerificationCode struct {
	ID          string    `json:"id" db:"id"`
	Email       string    `json:"email" db:"email"`
	CodeHash    string    `json:"-" db:"code_hash"`
	ExpiresAt   time.Time `json:"expiresAt" db:"expires_at"`
	Attempts    int       `json:"attempts" db:"attempts"`
	MaxAttempts int       `json:"maxAttempts" db:"max_attempts"`
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
}

type SendCodeResponse struct {
	VerificationID    string `json:"verificationId"`
	ExpiresAt         int64  `json:"expiresAt"`
	RemainingAttempts int    `json:"remainingAttempts"`
}

type VerifyCodeResponse struct {
	Verified bool `json:"verified"`
}

type VerificationConfig struct {
	CodeLength       int           // 6
	CodeExpiry       time.Duration // 10 minutes
	MaxAttempts      int           // 5 per verification ID
	MaxSendsPerEmail int           // 3 per 10 minutes
	ResendCooldown   time.Duration // 60 seconds
}
