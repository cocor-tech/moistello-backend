package contribution

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/moistello/backend/pkg/apperrors"
)

type dbExecutor interface {
	QueryRowxContext(ctx context.Context, query string, args ...interface{}) *sqlx.Row
	QueryxContext(ctx context.Context, query string, args ...interface{}) (*sqlx.Rows, error)
	NamedExecContext(ctx context.Context, query string, arg interface{}) (sql.Result, error)
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

type pgRepo struct {
	db dbExecutor
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

func NewRepositoryFromTx(tx *sqlx.Tx) Repository {
	return &pgRepo{db: tx}
}

func scanContribution(row interface{ Scan(...interface{}) error }) (*Contribution, error) {
	var c Contribution
	var txnHash sql.NullString
	err := row.Scan(
		&c.ID, &c.CircleID, &c.UserID, &c.RoundNumber, &c.Amount,
		&txnHash, &c.Status, &c.OnTime, &c.VerifiedOnchain, &c.VerificationStatus,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("scanning contribution row: %w", err)
	}
	c.TxnHash = txnHash
	return &c, nil
}

func (r *pgRepo) FindByID(ctx context.Context, id uuid.UUID) (*Contribution, error) {
	query := `SELECT id, circle_id, user_id, round_number, amount, txn_hash, status, on_time, verified_onchain, verification_status, created_at, updated_at
		FROM contributions WHERE id = $1`
	return scanContribution(r.db.QueryRowxContext(ctx, query, id))
}

func (r *pgRepo) FindByCircleAndUser(ctx context.Context, circleID, userID uuid.UUID) (*Contribution, error) {
	query := `SELECT id, circle_id, user_id, round_number, amount, txn_hash, status, on_time, verified_onchain, verification_status, created_at, updated_at
		FROM contributions WHERE circle_id = $1 AND user_id = $2 ORDER BY created_at DESC LIMIT 1`
	return scanContribution(r.db.QueryRowxContext(ctx, query, circleID, userID))
}

func (r *pgRepo) FindByTxnHash(ctx context.Context, txnHash string) (*Contribution, error) {
	query := `SELECT id, circle_id, user_id, round_number, amount, txn_hash, status, on_time, verified_onchain, verification_status, created_at, updated_at
		FROM contributions WHERE txn_hash = $1 LIMIT 1`
	return scanContribution(r.db.QueryRowxContext(ctx, query, txnHash))
}

func (r *pgRepo) Create(ctx context.Context, c *Contribution) error {
	query := `INSERT INTO contributions (id, circle_id, user_id, round_number, amount, txn_hash, status, on_time, verified_onchain, verification_status, created_at, updated_at)
		VALUES (:id, :circle_id, :user_id, :round_number, :amount, :txn_hash, :status, :on_time, :verified_onchain, :verification_status, :created_at, :updated_at)`
	_, err := r.db.NamedExecContext(ctx, query, c)
	if err != nil {
		if isUniqueViolationPg(err) {
			return apperrors.ErrConflict
		}
		return fmt.Errorf("creating contribution: %w", err)
	}
	return nil
}

func (r *pgRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status ContributionStatus, txnHash string) error {
	query := `UPDATE contributions SET status = $1, txn_hash = $2, updated_at = NOW() WHERE id = $3`
	result, err := r.db.ExecContext(ctx, query, status, txnHash, id)
	if err != nil {
		return fmt.Errorf("updating contribution status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

func (r *pgRepo) UpdateVerificationStatus(ctx context.Context, id uuid.UUID, verifiedOnchain bool, status VerificationStatus) error {
	query := `UPDATE contributions SET verified_onchain = $1, verification_status = $2, updated_at = NOW() WHERE id = $3`
	result, err := r.db.ExecContext(ctx, query, verifiedOnchain, status, id)
	if err != nil {
		return fmt.Errorf("updating contribution verification status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

func (r *pgRepo) ListByUser(ctx context.Context, userID uuid.UUID, page, limit int) ([]Contribution, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	var total int
	countQuery := `SELECT COUNT(*) FROM contributions WHERE user_id = $1`
	if err := r.db.QueryRowxContext(ctx, countQuery, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting user contributions: %w", err)
	}

	query := `SELECT id, circle_id, user_id, round_number, amount, txn_hash, status, on_time, verified_onchain, verification_status, created_at, updated_at
		FROM contributions WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	rows, err := r.db.QueryxContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing user contributions: %w", err)
	}
	defer rows.Close()

	var contributions []Contribution
	for rows.Next() {
		c, err := scanContribution(rows)
		if err != nil {
			return nil, 0, err
		}
		contributions = append(contributions, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating contributions: %w", err)
	}
	return contributions, total, nil
}

func (r *pgRepo) ListByCircle(ctx context.Context, circleID uuid.UUID, page, limit int) ([]Contribution, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	var total int
	countQuery := `SELECT COUNT(*) FROM contributions WHERE circle_id = $1`
	if err := r.db.QueryRowxContext(ctx, countQuery, circleID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting circle contributions: %w", err)
	}

	query := `SELECT id, circle_id, user_id, round_number, amount, txn_hash, status, on_time, verified_onchain, verification_status, created_at, updated_at
		FROM contributions WHERE circle_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	rows, err := r.db.QueryxContext(ctx, query, circleID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing circle contributions: %w", err)
	}
	defer rows.Close()

	var contributions []Contribution
	for rows.Next() {
		c, err := scanContribution(rows)
		if err != nil {
			return nil, 0, err
		}
		contributions = append(contributions, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating contributions: %w", err)
	}
	return contributions, total, nil
}

func isUniqueViolationPg(err error) bool {
	if err == nil {
		return false
	}
	if pqErr, ok := err.(*pq.Error); ok {
		return pqErr.Code == pq.ErrorCode("23505")
	}
	return false
}
