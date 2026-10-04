package user

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	ID                   uuid.UUID      `json:"id" db:"id"`
	WalletAddress        string         `json:"walletAddress" db:"wallet_address"`
	Email                *string        `json:"email,omitempty" db:"email"`
	Phone                *string        `json:"phone,omitempty" db:"phone"`
	DisplayName          *string        `json:"displayName,omitempty" db:"display_name"`
	AvatarIpfsHash       *string        `json:"avatarIpfsHash,omitempty" db:"avatar_ipfs_hash"`
	CountryCode          *string        `json:"countryCode,omitempty" db:"country_code"`
	PreferredLanguage    string         `json:"preferredLanguage" db:"preferred_language"`
	MoiScore             int            `json:"moiScore" db:"moi_score"`
	Role                 Role           `json:"role" db:"role"`
	SessionTTLMinutes    int            `json:"sessionTtlMinutes" db:"session_ttl_minutes"`
	PasswordHash         sql.NullString `json:"-" db:"password_hash"`
	TOTPSecret           sql.NullString `json:"-" db:"totp_secret"`
	TOTPEnabled          bool           `json:"totpEnabled" db:"totp_enabled"`
	BackupCodes          pq.StringArray `json:"-" db:"backup_codes"`
	EmailVerified        bool           `json:"emailVerified" db:"email_verified"`
	PasskeyCredentialID  *string        `json:"passkeyCredentialId,omitempty" db:"passkey_credential_id"`
	NotificationChannels pq.StringArray `json:"notificationChannels" db:"notification_channels"`
	NotificationsMuted   bool           `json:"notificationsMuted" db:"notifications_muted"`
	// DigestEnabled turns digest batching on for this user (#415). When true,
	// non-urgent circle events are collapsed into a periodic summary instead of
	// being delivered one by one. Urgent classes (payout, dispute, completed
	// circle, or a deadline under 24h) always bypass batching.
	DigestEnabled bool `json:"digestEnabled" db:"digest_enabled"`
	// DigestIntervalMinutes is the cadence at which batched events are
	// summarised, in minutes. Clamped to a supported range on write; see
	// notification.MinDigestInterval / MaxDigestInterval.
	DigestIntervalMinutes int        `json:"digestIntervalMinutes" db:"digest_interval_minutes"`
	PushToken             *string    `json:"pushToken,omitempty" db:"push_token"`
	CreatedAt             time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt             time.Time  `json:"updatedAt" db:"updated_at"`
	DeletedAt             *time.Time `json:"deletedAt,omitempty" db:"deleted_at"`
}

// HashEmail consistently hashes an email address for storage and lookup.
func HashEmail(email string) string {
	h := sha256.Sum256([]byte(email))
	return hex.EncodeToString(h[:])
}
