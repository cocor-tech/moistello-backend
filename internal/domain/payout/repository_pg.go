package payout

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/moistello/backend/pkg/apperrors"
)

type pgRepo struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

func scanPayout(row interface{ Scan(...interface{}) error }) (*Payout, error) {
	var p Payout
	var txnHash sql.NullString
	err := row.Scan(
		&p.ID, &p.CircleID, &p.RecipientID, &p.RoundNumber, &p.Amount,
		&p.FeeAmount, &txnHash, &p.PayoutType, &p.VerifiedOnchain, &p.VerificationStatus,
		&p.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("scanning payout row: %w", err)
	}
	p.TxnHash = txnHash
	return &p, nil
}

func (r *pgRepo) FindByID(ctx context.Context, id uuid.UUID) (*Payout, error) {
	query := `SELECT id, circle_id, recipient_id, round_number, amount, fee_amount, txn_hash, payout_type, verified_onchain, verification_status, created_at
		FROM payouts WHERE id = $1`
	return scanPayout(r.db.QueryRowxContext(ctx, query, id))
}

func (r *pgRepo) FindByTxnHash(ctx context.Context, txnHash string) (*Payout, error) {
	query := `SELECT id, circle_id, recipient_id, round_number, amount, fee_amount, txn_hash, payout_type, verified_onchain, verification_status, created_at
		FROM payouts WHERE txn_hash = $1 LIMIT 1`
	return scanPayout(r.db.QueryRowxContext(ctx, query, txnHash))
}

func (r *pgRepo) Create(ctx context.Context, p *Payout) error {
	query := `INSERT INTO payouts (id, circle_id, recipient_id, round_number, amount, fee_amount, txn_hash, payout_type, verified_onchain, verification_status, created_at)
		VALUES (:id, :circle_id, :recipient_id, :round_number, :amount, :fee_amount, :txn_hash, :payout_type, :verified_onchain, :verification_status, :created_at)`
	_, err := r.db.NamedExecContext(ctx, query, p)
	if err != nil {
		return fmt.Errorf("creating payout: %w", err)
	}
	return nil
}

func (r *pgRepo) UpdateVerificationStatus(ctx context.Context, id uuid.UUID, verifiedOnchain bool, status VerificationStatus) error {
	query := `UPDATE payouts SET verified_onchain = $1, verification_status = $2 WHERE id = $3`
	result, err := r.db.ExecContext(ctx, query, verifiedOnchain, status, id)
	if err != nil {
		return fmt.Errorf("updating payout verification status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

func (r *pgRepo) ListByUser(ctx context.Context, userID uuid.UUID, page, limit int) ([]Payout, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	var total int
	countQuery := `SELECT COUNT(*) FROM payouts WHERE recipient_id = $1`
	if err := r.db.QueryRowxContext(ctx, countQuery, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting user payouts: %w", err)
	}

	query := `SELECT id, circle_id, recipient_id, round_number, amount, fee_amount, txn_hash, payout_type, verified_onchain, verification_status, created_at
		FROM payouts WHERE recipient_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	rows, err := r.db.QueryxContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing user payouts: %w", err)
	}
	defer rows.Close()

	var payouts []Payout
	for rows.Next() {
		p, err := scanPayout(rows)
		if err != nil {
			return nil, 0, err
		}
		payouts = append(payouts, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating payouts: %w", err)
	}
	return payouts, total, nil
}

func (r *pgRepo) ListByCircle(ctx context.Context, circleID uuid.UUID, page, limit int) ([]Payout, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	var total int
	countQuery := `SELECT COUNT(*) FROM payouts WHERE circle_id = $1`
	if err := r.db.QueryRowxContext(ctx, countQuery, circleID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting circle payouts: %w", err)
	}

	query := `SELECT id, circle_id, recipient_id, round_number, amount, fee_amount, txn_hash, payout_type, verified_onchain, verification_status, created_at
		FROM payouts WHERE circle_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	rows, err := r.db.QueryxContext(ctx, query, circleID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing circle payouts: %w", err)
	}
	defer rows.Close()

	var payouts []Payout
	for rows.Next() {
		p, err := scanPayout(rows)
		if err != nil {
			return nil, 0, err
		}
		payouts = append(payouts, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating payouts: %w", err)
	}
	return payouts, total, nil
}
