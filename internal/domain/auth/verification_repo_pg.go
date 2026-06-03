package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/moistello/backend/pkg/apperrors"
)

func hashEmail(email string) string {
	h := sha256.Sum256([]byte(email))
	return hex.EncodeToString(h[:])
}

type verificationPGRepo struct {
	db *sqlx.DB
}

func NewVerificationRepository(db *sqlx.DB) VerificationStore {
	return &verificationPGRepo{db: db}
}

func (r *verificationPGRepo) Save(ctx context.Context, v *VerificationCode) error {
	query := `
		INSERT INTO email_verifications (id, email, code_hash, expires_at, attempts, max_attempts, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			code_hash = EXCLUDED.code_hash,
			expires_at = EXCLUDED.expires_at,
			attempts = EXCLUDED.attempts,
			updated_at = NOW()
	`
	_, err := r.db.ExecContext(ctx, query,
		v.ID, v.Email, v.CodeHash, v.ExpiresAt, v.Attempts, v.MaxAttempts, v.CreatedAt,
	)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return apperrors.ErrConflict
		}
		return fmt.Errorf("saving verification: %w", err)
	}
	return nil
}

func (r *verificationPGRepo) FindByID(ctx context.Context, id string) (*VerificationCode, error) {
	query := `
		SELECT id, email, code_hash, expires_at, attempts, max_attempts, created_at
		FROM email_verifications
		WHERE id = $1
	`
	var v VerificationCode
	err := r.db.QueryRowxContext(ctx, query, id).Scan(
		&v.ID, &v.Email, &v.CodeHash, &v.ExpiresAt, &v.Attempts, &v.MaxAttempts, &v.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("finding verification by id: %w", err)
	}
	return &v, nil
}

func (r *verificationPGRepo) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM email_verifications WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("deleting verification: %w", err)
	}
	return nil
}

func (r *verificationPGRepo) MarkEmailVerified(ctx context.Context, email string) error {
	hashedEmail := hashEmail(email)
	query := `
		INSERT INTO user_emails (email, email_verified, verified_at, created_at)
		VALUES ($1, TRUE, NOW(), NOW())
		ON CONFLICT (email) DO UPDATE SET
			email_verified = TRUE,
			verified_at = NOW()
	`
	_, err := r.db.ExecContext(ctx, query, hashedEmail)
	if err != nil {
		return fmt.Errorf("marking email verified: %w", err)
	}
	return nil
}

func (r *verificationPGRepo) IsEmailVerified(ctx context.Context, email string) (bool, error) {
	hashedEmail := hashEmail(email)
	query := `SELECT email_verified FROM user_emails WHERE email = $1`
	var verified bool
	err := r.db.QueryRowxContext(ctx, query, hashedEmail).Scan(&verified)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("checking email verified: %w", err)
	}
	return verified, nil
}

type emailVerificationRow struct {
	ID        string    `db:"id"`
	Email     string    `db:"email"`
	CodeHash  string    `db:"code_hash"`
	ExpiresAt time.Time `db:"expires_at"`
	Attempts  int       `db:"attempts"`
	MaxAttempts int    `db:"max_attempts"`
	CreatedAt time.Time `db:"created_at"`
}
