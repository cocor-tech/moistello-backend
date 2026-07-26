package user

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

type User struct {
	ID                     uuid.UUID      `json:"id" db:"id"`
	WalletAddress          string         `json:"walletAddress" db:"wallet_address"`
	Email                  *string        `json:"email,omitempty" db:"email"`
	Phone                  *string        `json:"phone,omitempty" db:"phone"`
	DisplayName            *string        `json:"displayName,omitempty" db:"display_name"`
	AvatarIpfsHash         *string        `json:"avatarIpfsHash,omitempty" db:"avatar_ipfs_hash"`
	CountryCode            *string        `json:"countryCode,omitempty" db:"country_code"`
	PreferredLanguage      string         `json:"preferredLanguage" db:"preferred_language"`
	MoiScore               int            `json:"moiScore" db:"moi_score"`
	Role                   Role           `json:"role" db:"role"`
	SessionTTLMinutes      int            `json:"sessionTtlMinutes" db:"session_ttl_minutes"`
	PasswordHash           sql.NullString `json:"-" db:"password_hash"`
	TOTPSecret             sql.NullString `json:"-" db:"totp_secret"`
	TOTPEnabled            bool           `json:"totpEnabled" db:"totp_enabled"`
	BackupCodes            pq.StringArray `json:"-" db:"backup_codes"`
	EmailVerified          bool           `json:"emailVerified" db:"email_verified"`
	PasskeyCredentialID    *string        `json:"passkeyCredentialId,omitempty" db:"passkey_credential_id"`
	NotificationChannels   pq.StringArray `json:"notificationChannels" db:"notification_channels"`
	NotificationsMuted     bool           `json:"notificationsMuted" db:"notifications_muted"`
	CreatedAt              time.Time      `json:"createdAt" db:"created_at"`
	UpdatedAt              time.Time      `json:"updatedAt" db:"updated_at"`
}
