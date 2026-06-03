package user

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/moistello/backend/pkg/apperrors"
)

func hashUserEmail(email string) string {
	h := sha256.Sum256([]byte(email))
	return hex.EncodeToString(h[:])
}

type pgRepo struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

func scanUser(row interface{ Scan(...interface{}) error }) (*User, error) {
	var u User
	var email, phone, displayName, avatarIpfsHash, kycProviderRef, countryCode sql.NullString
	err := row.Scan(
		&u.ID,
		&u.WalletAddress,
		&email,
		&phone,
		&displayName,
		&avatarIpfsHash,
		&u.KYCStatus,
		&kycProviderRef,
		&countryCode,
		&u.PreferredLanguage,
		&u.MoiScore,
		&u.Role,
		&u.CreatedAt,
		&u.UpdatedAt,
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
	if kycProviderRef.Valid {
		u.KYCProviderRef = &kycProviderRef.String
	}
	if countryCode.Valid {
		u.CountryCode = &countryCode.String
	}
	return &u, nil
}

func (r *pgRepo) FindByID(ctx context.Context, id uuid.UUID) (*User, error) {
	query := `SELECT id, wallet_address, email, phone, display_name, avatar_ipfs_hash,
		kyc_status, kyc_provider_ref, country_code, preferred_language, moi_score, role,
		created_at, updated_at FROM users WHERE id = $1`
	return scanUser(r.db.QueryRowxContext(ctx, query, id))
}

func (r *pgRepo) FindByWalletAddress(ctx context.Context, walletAddress string) (*User, error) {
	query := `SELECT id, wallet_address, email, phone, display_name, avatar_ipfs_hash,
		kyc_status, kyc_provider_ref, country_code, preferred_language, moi_score, role,
		created_at, updated_at FROM users WHERE wallet_address = $1`
	return scanUser(r.db.QueryRowxContext(ctx, query, walletAddress))
}

func (r *pgRepo) FindByEmail(ctx context.Context, email string) (*User, error) {
	hashedEmail := hashUserEmail(email)
	query := `SELECT id, wallet_address, email, phone, display_name, avatar_ipfs_hash,
		kyc_status, kyc_provider_ref, country_code, preferred_language, moi_score, role,
		created_at, updated_at FROM users WHERE email = $1`
	return scanUser(r.db.QueryRowxContext(ctx, query, hashedEmail))
}

func (r *pgRepo) EmailPreviouslyVerified(ctx context.Context, email string) (bool, error) {
	hashedEmail := hashUserEmail(email)
	var count int
	err := r.db.GetContext(ctx, &count, "SELECT COUNT(*) FROM user_emails WHERE email = $1 AND email_verified = true", hashedEmail)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *pgRepo) Create(ctx context.Context, u *User) error {
	query := `INSERT INTO users (id, wallet_address, email, phone, display_name,
		avatar_ipfs_hash, kyc_status, kyc_provider_ref, country_code, preferred_language,
		moi_score, role, created_at, updated_at)
		VALUES (:id, :wallet_address, :email, :phone, :display_name,
		:avatar_ipfs_hash, :kyc_status, :kyc_provider_ref, :country_code, :preferred_language,
		:moi_score, :role, :created_at, :updated_at)`
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
		avatar_ipfs_hash = :avatar_ipfs_hash, kyc_status = :kyc_status, kyc_provider_ref = :kyc_provider_ref,
		country_code = :country_code, preferred_language = :preferred_language, moi_score = :moi_score,
		role = :role, updated_at = :updated_at WHERE id = :id`
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

func (r *pgRepo) UpdateKYCStatus(ctx context.Context, id uuid.UUID, status KYCStatus, providerRef string) error {
	query := `UPDATE users SET kyc_status = $1, kyc_provider_ref = $2, updated_at = NOW() WHERE id = $3`
	result, err := r.db.ExecContext(ctx, query, status, providerRef, id)
	if err != nil {
		return fmt.Errorf("updating kyc status: %w", err)
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

	var conditions []string
	var args []interface{}
	argIdx := 1

	if filter.Search != "" {
		conditions = append(conditions, fmt.Sprintf("(display_name ILIKE $%d OR wallet_address ILIKE $%d OR email ILIKE $%d)", argIdx, argIdx+1, argIdx+2))
		searchPattern := "%" + filter.Search + "%"
		args = append(args, searchPattern, searchPattern, searchPattern)
		argIdx += 3
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf(`SELECT id, wallet_address, email, phone, display_name, avatar_ipfs_hash,
		kyc_status, kyc_provider_ref, country_code, preferred_language, moi_score, role,
		created_at, updated_at FROM users %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		whereClause, argIdx, argIdx+1)
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
	var conditions []string
	var args []interface{}
	argIdx := 1

	if filter.Search != "" {
		conditions = append(conditions, fmt.Sprintf("(display_name ILIKE $%d OR wallet_address ILIKE $%d OR email ILIKE $%d)", argIdx, argIdx+1, argIdx+2))
		searchPattern := "%" + filter.Search + "%"
		args = append(args, searchPattern, searchPattern, searchPattern)
		argIdx += 3
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf("SELECT COUNT(*) FROM users %s", whereClause)
	var count int
	err := r.db.QueryRowxContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
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
