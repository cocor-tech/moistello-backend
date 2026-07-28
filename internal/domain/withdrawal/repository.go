package withdrawal

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type Repository interface {
	Create(ctx context.Context, w *Withdrawal) error
	GetByID(ctx context.Context, id string) (*Withdrawal, error)
	GetByUserID(ctx context.Context, userID string, limit int, offset int) ([]Withdrawal, error)
	UpdateStatus(ctx context.Context, id string, status WithdrawalStatus) error
	UpdateUSDCTxHash(ctx context.Context, id string, txHash string, receivedAt time.Time) error
	UpdateYellowCardTxID(ctx context.Context, id string, txID string) error
	MarkCompleted(ctx context.Context, id string, completedAt time.Time) error
	MarkFailed(ctx context.Context, id string, reason string) error
}

type pgRepo struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

func (r *pgRepo) Create(ctx context.Context, w *Withdrawal) error {
	query := `
		INSERT INTO withdrawals (
			id, user_id, amount_usdc, estimated_ngn, bank_code, account_number, 
			account_name, status, platform_address, payment_ref, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err := r.db.ExecContext(ctx, query,
		w.ID, w.UserID, w.AmountUSDC, w.EstimatedNGN, w.BankCode, w.AccountNumber,
		w.AccountName, w.Status, w.PlatformAddress, w.PaymentRef, w.CreatedAt)
	if err != nil {
		return fmt.Errorf("creating withdrawal: %w", err)
	}
	return nil
}

func (r *pgRepo) GetByID(ctx context.Context, id string) (*Withdrawal, error) {
	var w Withdrawal
	query := `
		SELECT id, user_id, amount_usdc, estimated_ngn, bank_code, account_number,
			   account_name, status, platform_address, usdc_tx_hash, yellow_card_tx_id,
			   created_at, received_at, completed_at, failure_reason, payment_ref
		FROM withdrawals
		WHERE id = $1
	`
	err := r.db.GetContext(ctx, &w, query, id)
	if err != nil {
		return nil, fmt.Errorf("getting withdrawal by id: %w", err)
	}
	return &w, nil
}

func (r *pgRepo) GetByUserID(ctx context.Context, userID string, limit int, offset int) ([]Withdrawal, error) {
	var withdrawals []Withdrawal
	query := `
		SELECT id, user_id, amount_usdc, estimated_ngn, bank_code, account_number,
			   account_name, status, platform_address, usdc_tx_hash, yellow_card_tx_id,
			   created_at, received_at, completed_at, failure_reason, payment_ref
		FROM withdrawals
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`
	err := r.db.SelectContext(ctx, &withdrawals, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("getting withdrawals by user: %w", err)
	}
	return withdrawals, nil
}

func (r *pgRepo) UpdateStatus(ctx context.Context, id string, status WithdrawalStatus) error {
	query := `UPDATE withdrawals SET status = $1 WHERE id = $2`
	_, err := r.db.ExecContext(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("updating withdrawal status: %w", err)
	}
	return nil
}

func (r *pgRepo) UpdateUSDCTxHash(ctx context.Context, id string, txHash string, receivedAt time.Time) error {
	query := `
		UPDATE withdrawals 
		SET usdc_tx_hash = $1, status = $2, received_at = $3 
		WHERE id = $4
	`
	_, err := r.db.ExecContext(ctx, query, txHash, WithdrawalStatusReceived, receivedAt, id)
	if err != nil {
		return fmt.Errorf("updating usdc tx hash: %w", err)
	}
	return nil
}

func (r *pgRepo) UpdateYellowCardTxID(ctx context.Context, id string, txID string) error {
	query := `
		UPDATE withdrawals 
		SET yellow_card_tx_id = $1, status = $2 
		WHERE id = $3
	`
	_, err := r.db.ExecContext(ctx, query, txID, WithdrawalStatusConverting, id)
	if err != nil {
		return fmt.Errorf("updating yellow card tx id: %w", err)
	}
	return nil
}

func (r *pgRepo) MarkCompleted(ctx context.Context, id string, completedAt time.Time) error {
	query := `
		UPDATE withdrawals 
		SET status = $1, completed_at = $2 
		WHERE id = $3
	`
	_, err := r.db.ExecContext(ctx, query, WithdrawalStatusCompleted, completedAt, id)
	if err != nil {
		return fmt.Errorf("marking withdrawal completed: %w", err)
	}
	return nil
}

func (r *pgRepo) MarkFailed(ctx context.Context, id string, reason string) error {
	query := `
		UPDATE withdrawals 
		SET status = $1, failure_reason = $2 
		WHERE id = $3
	`
	_, err := r.db.ExecContext(ctx, query, WithdrawalStatusFailed, reason, id)
	if err != nil {
		return fmt.Errorf("marking withdrawal failed: %w", err)
	}
	return nil
}
