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
