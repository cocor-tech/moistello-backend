package user

import (
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

type User struct {
	ID                uuid.UUID `json:"id" db:"id"`
	WalletAddress     string    `json:"walletAddress" db:"wallet_address"`
	Email             *string   `json:"email,omitempty" db:"email"`
	Phone             *string   `json:"phone,omitempty" db:"phone"`
	DisplayName       *string   `json:"displayName,omitempty" db:"display_name"`
	AvatarIpfsHash    *string   `json:"avatarIpfsHash,omitempty" db:"avatar_ipfs_hash"`
	CountryCode       *string   `json:"countryCode,omitempty" db:"country_code"`
	PreferredLanguage string    `json:"preferredLanguage" db:"preferred_language"`
	MoiScore          int       `json:"moiScore" db:"moi_score"`
	Role              Role      `json:"role" db:"role"`
	CreatedAt         time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt         time.Time `json:"updatedAt" db:"updated_at"`
}
