package treasury

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type Repository interface {
	GetBalance(ctx context.Context) (*TreasuryBalance, error)
	CreateWithdrawalRequest(ctx context.Context, req *WithdrawalRequest) error
	GetWithdrawalRequest(ctx context.Context, id string) (*WithdrawalRequest, error)
	ListWithdrawalRequests(ctx context.Context, status WithdrawalStatus, limit int) ([]WithdrawalRequest, error)
	UpdateWithdrawalRequest(ctx context.Context, id string, status WithdrawalStatus, approvedBy string, txHash string, failureReason string) error
	ListTransactions(ctx context.Context, limit int, offset int) ([]TreasuryTransaction, error)
	CountTransactions(ctx context.Context) (int, error)
}

type pgRepo struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

func (r *pgRepo) GetBalance(ctx context.Context) (*TreasuryBalance, error) {
	var balance TreasuryBalance
	query := `
		SELECT 
			COALESCE(SUM(CASE WHEN asset = 'XLM' THEN amount ELSE 0 END), '0') as xlm,
			COALESCE(SUM(CASE WHEN asset = 'USDC' THEN amount ELSE 0 END), '0') as usdc
		FROM treasury_transactions
		WHERE type IN ('deposit', 'fee')
	`
	err := r.db.GetContext(ctx, &balance, query)
	if err != nil {
		return nil, fmt.Errorf("getting treasury balance: %w", err)
	}
	return &balance, nil
}

func (r *pgRepo) CreateWithdrawalRequest(ctx context.Context, req *WithdrawalRequest) error {
	query := `
		INSERT INTO treasury_withdrawal_requests (id, destination, asset, amount, status, approval_method, requested_by, requested_at, timelock_expiry, multi_sig_signatures)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.db.ExecContext(ctx, query,
		req.ID, req.Destination, req.Asset, req.Amount, req.Status, req.ApprovalMethod,
		req.RequestedBy, req.RequestedAt, req.TimelockExpiry, req.MultiSigSignatures)
	if err != nil {
		return fmt.Errorf("creating withdrawal request: %w", err)
	}
	return nil
}

func (r *pgRepo) GetWithdrawalRequest(ctx context.Context, id string) (*WithdrawalRequest, error) {
	var req WithdrawalRequest
	query := `
		SELECT id, destination, asset, amount, status, approval_method, requested_by, requested_at,
			   approved_by, approved_at, tx_hash, failure_reason, executed_at, timelock_expiry, multi_sig_signatures
		FROM treasury_withdrawal_requests
		WHERE id = $1
	`
	err := r.db.GetContext(ctx, &req, query, id)
	if err != nil {
		return nil, fmt.Errorf("getting withdrawal request: %w", err)
	}
	return &req, nil
}

func (r *pgRepo) ListWithdrawalRequests(ctx context.Context, status WithdrawalStatus, limit int) ([]WithdrawalRequest, error) {
	var requests []WithdrawalRequest
	query := `
		SELECT id, destination, asset, amount, status, approval_method, requested_by, requested_at,
			   approved_by, approved_at, tx_hash, failure_reason, executed_at, timelock_expiry, multi_sig_signatures
		FROM treasury_withdrawal_requests
	`
	args := []interface{}{}
	if status != "" {
		query += " WHERE status = $1"
		args = append(args, status)
	}
	query += " ORDER BY requested_at DESC LIMIT $" + fmt.Sprintf("%d", len(args)+1)
	args = append(args, limit)

	err := r.db.SelectContext(ctx, &requests, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing withdrawal requests: %w", err)
	}
	return requests, nil
}

func (r *pgRepo) UpdateWithdrawalRequest(ctx context.Context, id string, status WithdrawalStatus, approvedBy string, txHash string, failureReason string) error {
	query := `
		UPDATE treasury_withdrawal_requests
		SET status = $1, approved_by = $2, approved_at = $3, tx_hash = $4, failure_reason = $5, executed_at = $6
		WHERE id = $7
	`
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, query, status, approvedBy, now, txHash, failureReason, now, id)
	if err != nil {
		return fmt.Errorf("updating withdrawal request: %w", err)
	}
	return nil
}

func (r *pgRepo) ListTransactions(ctx context.Context, limit int, offset int) ([]TreasuryTransaction, error) {
	var transactions []TreasuryTransaction
	query := `
		SELECT id, type, asset, amount, from_address, to_address, tx_hash, description, created_at
		FROM treasury_transactions
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`
	err := r.db.SelectContext(ctx, &transactions, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing treasury transactions: %w", err)
	}
	return transactions, nil
}

func (r *pgRepo) CountTransactions(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM treasury_transactions`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting treasury transactions: %w", err)
	}
	return count, nil
}
