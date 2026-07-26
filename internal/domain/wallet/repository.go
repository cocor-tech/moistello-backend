package wallet

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/google/uuid"
)

type WithdrawalRecord struct {
	ID          uuid.UUID `db:"id"`
	UserID      uuid.UUID `db:"user_id"`
	Destination string    `db:"destination"`
	Asset       string    `db:"asset"`
	Amount      float64   `db:"amount"`
	Status      string    `db:"status"`
	IPAddress   string    `db:"ip_address"`
	UserAgent   string    `db:"user_agent"`
	Failure     string    `db:"failure_reason"`
	TxHash      string    `db:"tx_hash"`
	CreatedAt   time.Time `db:"created_at"`
}

type Repository interface {
	Create(ctx context.Context, w *Wallet) error
	FindByID(ctx context.Context, id string) (*Wallet, error)
	FindByUserID(ctx context.Context, userID string) ([]Wallet, error)
	FindByPublicKey(ctx context.Context, publicKey string) (*Wallet, error)
	Delete(ctx context.Context, id string) error
	// DeleteByOwner deletes a wallet only when both walletID and userID match,
	// preventing IDOR by enforcing ownership at the SQL level.
	DeleteByOwner(ctx context.Context, walletID, userID string) error

	// Security methods
	CheckRateLimit(ctx context.Context, userID uuid.UUID) (bool, error)
	IncrementRateLimit(ctx context.Context, userID uuid.UUID) error
	GetDailySpending(ctx context.Context, userID uuid.UUID) (float64, error)
	RecordWithdrawalAudit(ctx context.Context, r *WithdrawalRecord) error
}

type pgRepo struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

func (r *pgRepo) Create(ctx context.Context, w *Wallet) error {
	query := `
		INSERT INTO wallets (user_id, public_key, encrypted_secret_key, encryption_nonce, wallet_type, is_primary)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`
	return r.db.QueryRowContext(ctx, query,
		w.UserID, w.PublicKey, w.EncryptedSecretKey, w.EncryptionNonce,
		w.WalletType, w.IsPrimary,
	).Scan(&w.ID, &w.CreatedAt, &w.UpdatedAt)
}

func (r *pgRepo) FindByID(ctx context.Context, id string) (*Wallet, error) {
	var w Wallet
	err := r.db.GetContext(ctx, &w,
		`SELECT id, user_id, public_key, encrypted_secret_key, encryption_nonce, wallet_type, is_primary, created_at, updated_at
		 FROM wallets WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("finding wallet by id: %w", err)
	}
	return &w, nil
}

func (r *pgRepo) FindByUserID(ctx context.Context, userID string) ([]Wallet, error) {
	var wallets []Wallet
	err := r.db.SelectContext(ctx, &wallets,
		`SELECT id, user_id, public_key, wallet_type, is_primary, created_at, updated_at
		 FROM wallets WHERE user_id = $1 ORDER BY is_primary DESC, created_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("finding wallets by user: %w", err)
	}
	return wallets, nil
}

func (r *pgRepo) FindByPublicKey(ctx context.Context, publicKey string) (*Wallet, error) {
	var w Wallet
	err := r.db.GetContext(ctx, &w,
		`SELECT id, user_id, public_key, encrypted_secret_key, encryption_nonce, wallet_type, is_primary, created_at, updated_at
		 FROM wallets WHERE public_key = $1`, publicKey)
	if err != nil {
		return nil, fmt.Errorf("finding wallet by public key: %w", err)
	}
	return &w, nil
}

func (r *pgRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM wallets WHERE id = $1`, id)
	return err
}

// DeleteByOwner deletes a wallet only when both walletID and userID match,
// scoping the DELETE to enforce ownership at the database level.
func (r *pgRepo) DeleteByOwner(ctx context.Context, walletID, userID string) error {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM wallets WHERE id = $1 AND user_id = $2`, walletID, userID)
	if err != nil {
		return fmt.Errorf("deleting wallet: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking delete result: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("wallet not found or does not belong to user")
	}
	return nil
}

func (r *pgRepo) CheckRateLimit(ctx context.Context, userID uuid.UUID) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM withdrawal_rate_limits
		WHERE user_id = $1 AND window_start > NOW() - INTERVAL '1 hour'`, userID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count < 3, nil // max 3 withdrawals per hour
}

func (r *pgRepo) IncrementRateLimit(ctx context.Context, userID uuid.UUID) error {
	// Upsert: if row exists within last hour, increment; otherwise insert new
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO withdrawal_rate_limits (user_id, window_start, attempt_count)
		VALUES ($1, NOW(), 1)
		ON CONFLICT (user_id, window_start)
		DO UPDATE SET attempt_count = withdrawal_rate_limits.attempt_count + 1
		WHERE withdrawal_rate_limits.window_start > NOW() - INTERVAL '1 hour'`, userID)
	return err
}

func (r *pgRepo) GetDailySpending(ctx context.Context, userID uuid.UUID) (float64, error) {
	var total sql.NullFloat64
	err := r.db.QueryRowContext(ctx, `
		SELECT SUM(amount) FROM withdrawal_audit
		WHERE user_id = $1 AND status = 'completed'
		AND created_at > NOW() - INTERVAL '24 hours'`, userID).Scan(&total)
	if err != nil {
		return 0, err
	}
	if total.Valid {
		return total.Float64, nil
	}
	return 0, nil
}

func (r *pgRepo) RecordWithdrawalAudit(ctx context.Context, rec *WithdrawalRecord) error {
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO withdrawal_audit (id, user_id, destination, asset, amount, status, ip_address, user_agent, failure_reason, tx_hash, created_at)
		VALUES (:id, :user_id, :destination, :asset, :amount, :status, :ip_address, :user_agent, :failure_reason, :tx_hash, :created_at)`, rec)
	return err
}
