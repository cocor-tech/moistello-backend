package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/moistello/backend/pkg/apperrors"
)

type pgRepo struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

// userColumns is the canonical projection for reading a user. Every lookup
// shares it so a column added here is picked up everywhere at once, and the
// scan order below matches it exactly.
const userColumns = `id, wallet_address, email, phone, display_name, avatar_ipfs_hash,
		country_code, preferred_language, moi_score, role,
		session_ttl_minutes, password_hash, totp_secret, totp_enabled, backup_codes, email_verified, passkey_credential_id,
		notification_channels, notifications_muted, digest_enabled, digest_interval_minutes, push_token,
		created_at, updated_at, deleted_at`

// scanUser reads the userColumns projection. deleted_at is read into the
// struct so administrative tooling can see when a user was deleted; it is
// always NULL on the paths that filter on deleted_at IS NULL.
func scanUser(row interface{ Scan(...interface{}) error }) (*User, error) {
	var u User
	var email, phone, displayName, avatarIpfsHash, countryCode, passkeyCredentialID, totpSecret, passwordHash, pushToken sql.NullString
	var deletedAt sql.NullTime
	err := row.Scan(
		&u.ID,
		&u.WalletAddress,
		&email,
		&phone,
		&displayName,
		&avatarIpfsHash,
		&countryCode,
		&u.PreferredLanguage,
		&u.MoiScore,
		&u.Role,
		&u.SessionTTLMinutes,
		&passwordHash,
		&totpSecret,
		&u.TOTPEnabled,
		&u.BackupCodes,
		&u.EmailVerified,
		&passkeyCredentialID,
		&u.NotificationChannels,
		&u.NotificationsMuted,
		&u.DigestEnabled,
		&u.DigestIntervalMinutes,
		&pushToken,
		&u.CreatedAt,
		&u.UpdatedAt,
		&deletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("scanning user row: %w", err)
	}
	if email.Valid {
		u.Email = &email.String
	}
	if phone.Valid {
		u.Phone = &phone.String
	}
	if displayName.Valid {
		u.DisplayName = &displayName.String
	}
	if avatarIpfsHash.Valid {
		u.AvatarIpfsHash = &avatarIpfsHash.String
	}
	if countryCode.Valid {
		u.CountryCode = &countryCode.String
	}
	if passwordHash.Valid {
		u.PasswordHash = passwordHash
	}
	if totpSecret.Valid {
		u.TOTPSecret = totpSecret
	}
	if passkeyCredentialID.Valid {
		u.PasskeyCredentialID = &passkeyCredentialID.String
	}
	if pushToken.Valid {
		u.PushToken = &pushToken.String
	}
	if deletedAt.Valid {
		t := deletedAt.Time
		u.DeletedAt = &t
	}
	return &u, nil
}

func (r *pgRepo) FindByID(ctx context.Context, id uuid.UUID) (*User, error) {
	query := `SELECT ` + userColumns + ` FROM users WHERE id = $1 AND deleted_at IS NULL`
	return scanUser(r.db.QueryRowxContext(ctx, query, id))
}

func (r *pgRepo) FindByWalletAddress(ctx context.Context, walletAddress string) (*User, error) {
	query := `SELECT ` + userColumns + ` FROM users WHERE wallet_address = $1 AND deleted_at IS NULL`
	return scanUser(r.db.QueryRowxContext(ctx, query, walletAddress))
}

func (r *pgRepo) FindByEmail(ctx context.Context, email string) (*User, error) {
	hashedEmail := HashEmail(email)
	query := `SELECT ` + userColumns + ` FROM users WHERE email = $1 AND deleted_at IS NULL`
	return scanUser(r.db.QueryRowxContext(ctx, query, hashedEmail))
}

func (r *pgRepo) FindByPasskeyCredentialID(ctx context.Context, credentialID string) (*User, error) {
	query := `SELECT ` + userColumns + ` FROM users WHERE passkey_credential_id = $1 AND deleted_at IS NULL`
	return scanUser(r.db.QueryRowxContext(ctx, query, credentialID))
}

// FindByIDIncludingDeleted is the administrative read path: it bypasses the
// soft-delete filter so an admin can inspect a deleted account. It is not
// reachable from authentication (see Restore for re-admitting a user).
func (r *pgRepo) FindByIDIncludingDeleted(ctx context.Context, id uuid.UUID) (*User, error) {
	query := `SELECT ` + userColumns + ` FROM users WHERE id = $1`
	return scanUser(r.db.QueryRowxContext(ctx, query, id))
}

func (r *pgRepo) Create(ctx context.Context, u *User) error {
	query := `INSERT INTO users (id, wallet_address, email, phone, display_name,
		avatar_ipfs_hash, country_code, preferred_language,
		moi_score, role, session_ttl_minutes, password_hash, totp_secret, totp_enabled, backup_codes, email_verified, passkey_credential_id,
		created_at, updated_at)
		VALUES (:id, :wallet_address, :email, :phone, :display_name,
		:avatar_ipfs_hash, :country_code, :preferred_language,
		:moi_score, :role, :session_ttl_minutes, :password_hash, :totp_secret, :totp_enabled, :backup_codes, :email_verified, :passkey_credential_id,
		:created_at, :updated_at)`
	_, err := r.db.NamedExecContext(ctx, query, u)
	if err != nil {
		if isUniqueViolation(err) {
			return apperrors.ErrConflict
		}
		return fmt.Errorf("creating user: %w", err)
	}
	return nil
}

func (r *pgRepo) Update(ctx context.Context, u *User) error {
	query := `UPDATE users SET email = :email, phone = :phone, display_name = :display_name,
		avatar_ipfs_hash = :avatar_ipfs_hash,
		country_code = :country_code, preferred_language = :preferred_language, moi_score = :moi_score,
		role = :role, session_ttl_minutes = :session_ttl_minutes, password_hash = :password_hash,
		totp_secret = :totp_secret, totp_enabled = :totp_enabled,
		backup_codes = :backup_codes, email_verified = :email_verified,
		passkey_credential_id = :passkey_credential_id,
		notification_channels = :notification_channels, notifications_muted = :notifications_muted,
		digest_enabled = :digest_enabled, digest_interval_minutes = :digest_interval_minutes,
		updated_at = :updated_at WHERE id = :id`
	result, err := r.db.NamedExecContext(ctx, query, u)
	if err != nil {
		return fmt.Errorf("updating user: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (r *pgRepo) UpdateMoiScore(ctx context.Context, id uuid.UUID, score int) error {
	query := `UPDATE users SET moi_score = $1, updated_at = NOW() WHERE id = $2`
	result, err := r.db.ExecContext(ctx, query, score, id)
	if err != nil {
		return fmt.Errorf("updating moi score: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (r *pgRepo) List(ctx context.Context, filter UserFilter) ([]User, error) {
	page, limit := 1, 20
	if filter.Page > 0 {
		page = filter.Page
	}
	if filter.Limit > 0 && filter.Limit <= 100 {
		limit = filter.Limit
	}
	offset := (page - 1) * limit

	query := `SELECT ` + userColumns + ` FROM users WHERE deleted_at IS NULL`

	var args []interface{}
	if filter.Search != "" {
		query += ` AND (display_name ILIKE $1 OR wallet_address ILIKE $2 OR email ILIKE $3)`
		searchPattern := "%" + filter.Search + "%"
		args = append(args, searchPattern, searchPattern, searchPattern)
	}

	query += ` ORDER BY created_at DESC LIMIT $` + fmt.Sprint(len(args)+1) + ` OFFSET $` + fmt.Sprint(len(args)+2)
	args = append(args, limit, offset)

	rows, err := r.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating users: %w", err)
	}
	return users, nil
}

func (r *pgRepo) Count(ctx context.Context, filter UserFilter) (int, error) {
	query := "SELECT COUNT(*) FROM users WHERE deleted_at IS NULL"
	var args []interface{}

	if filter.Search != "" {
		query += " AND (display_name ILIKE $1 OR wallet_address ILIKE $1 OR email ILIKE $1)"
		searchPattern := "%" + filter.Search + "%"
		args = append(args, searchPattern)
	}

	var count int
	err := r.db.QueryRowxContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return count, nil
}

func (r *pgRepo) ClaimNextName(ctx context.Context) (int64, error) {
	var value int64
	err := r.db.QueryRowxContext(ctx, `UPDATE user_name_counter SET value = value + 1 WHERE id = 1 RETURNING value`).Scan(&value)
	if err != nil {
		return 0, fmt.Errorf("claiming name: %w", err)
	}
	return value, nil
}

// Delete soft-deletes the user by stamping deleted_at. The row is retained so
// foreign keys and the audit trail survive (#347).
func (r *pgRepo) Delete(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE users SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL`
	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		// Either no such user, or it was already soft-deleted. Distinguish so a
		// repeated delete is not reported as deleting a live account.
		var deletedAt sql.NullTime
		err := r.db.QueryRowxContext(ctx, `SELECT deleted_at FROM users WHERE id = $1`, id).Scan(&deletedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("checking user deletion state: %w", err)
		}
		if deletedAt.Valid {
			return ErrUserNotDeleted
		}
		return ErrUserNotFound
	}
	return nil
}

// Restore clears deleted_at, returning a soft-deleted user to the active set
// (#347). It deliberately does not touch any other column, so the audit trail
// and all related records stay exactly as they were at deletion time.
func (r *pgRepo) Restore(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE users SET deleted_at = NULL, updated_at = NOW() WHERE id = $1 AND deleted_at IS NOT NULL`
	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("restoring user: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		// Nothing was soft-deleted under that id: work out which case this is.
		var deletedAt sql.NullTime
		err := r.db.QueryRowxContext(ctx, `SELECT deleted_at FROM users WHERE id = $1`, id).Scan(&deletedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("checking user deletion state: %w", err)
		}
		return ErrUserNotDeleted
	}
	return nil
}

// ListDeleted returns soft-deleted users for administrative review. It is the
// exact inverse filter of List and must never be used to serve a normal user
// lookup.
func (r *pgRepo) ListDeleted(ctx context.Context, filter UserFilter) ([]User, error) {
	page, limit := 1, 20
	if filter.Page > 0 {
		page = filter.Page
	}
	if filter.Limit > 0 && filter.Limit <= 100 {
		limit = filter.Limit
	}
	offset := (page - 1) * limit

	query := `SELECT ` + userColumns + ` FROM users WHERE deleted_at IS NOT NULL`

	var args []interface{}
	if filter.Search != "" {
		query += ` AND (display_name ILIKE $1 OR wallet_address ILIKE $2 OR email ILIKE $3)`
		searchPattern := "%" + filter.Search + "%"
		args = append(args, searchPattern, searchPattern, searchPattern)
	}

	query += ` ORDER BY deleted_at DESC LIMIT $` + fmt.Sprint(len(args)+1) + ` OFFSET $` + fmt.Sprint(len(args)+2)
	args = append(args, limit, offset)

	rows, err := r.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing deleted users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating deleted users: %w", err)
	}
	return users, nil
}

// CountDeleted counts soft-deleted users for pagination metadata.
func (r *pgRepo) CountDeleted(ctx context.Context, filter UserFilter) (int, error) {
	query := "SELECT COUNT(*) FROM users WHERE deleted_at IS NOT NULL"
	var args []interface{}

	if filter.Search != "" {
		query += " AND (display_name ILIKE $1 OR wallet_address ILIKE $1 OR email ILIKE $1)"
		args = append(args, "%"+filter.Search+"%")
	}

	var count int
	if err := r.db.QueryRowxContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting deleted users: %w", err)
	}
	return count, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if pqErr, ok := err.(*pq.Error); ok {
		return pqErr.Code == pq.ErrorCode("23505")
	}
	return false
}
