package swap

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/moistello/backend/pkg/apperrors"
)

type PostgresRepository struct {
	db *sqlx.DB
}

func NewPostgresRepository(db *sqlx.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) CreateSwapOffer(ctx context.Context, offer *SwapOffer) error {
	query := `
		INSERT INTO swap_offers (
			id, circle_id, offeror_user_id, offeree_user_id,
			offeror_asset, offeror_amount, requested_asset, requested_amount,
			status, expires_at, created_at, updated_at
		) VALUES (
			:id, :circle_id, :offeror_user_id, :offeree_user_id,
			:offeror_asset, :offeror_amount, :requested_asset, :requested_amount,
			:status, :expires_at, :created_at, :updated_at
		)
	`
	_, err := r.db.NamedExecContext(ctx, query, offer)
	return err
}

func (r *PostgresRepository) GetSwapOfferByID(ctx context.Context, id string) (*SwapOffer, error) {
	query := `
		SELECT id, circle_id, offeror_user_id, offeree_user_id,
		       offeror_asset, offeror_amount, requested_asset, requested_amount,
		       status, transaction_hash, expires_at, created_at, updated_at
		FROM swap_offers WHERE id = $1
	`
	var offer SwapOffer
	err := r.db.GetContext(ctx, &offer, query, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperrors.ErrNotFound
	}
	return &offer, err
}

func (r *PostgresRepository) UpdateSwapOfferStatus(ctx context.Context, id string, status SwapOfferStatus, transactionHash *string) error {
	query := `
		UPDATE swap_offers
		SET status = $1, transaction_hash = $2, updated_at = NOW()
		WHERE id = $3
	`
	res, err := r.db.ExecContext(ctx, query, status, transactionHash, id)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) CompareAndSwapStatus(ctx context.Context, id string, expectedStatus, newStatus SwapOfferStatus, transactionHash *string) (bool, error) {
	query := `
		UPDATE swap_offers
		SET status = $1, transaction_hash = $2, updated_at = NOW()
		WHERE id = $3 AND status = $4
	`
	res, err := r.db.ExecContext(ctx, query, newStatus, transactionHash, id, expectedStatus)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (r *PostgresRepository) ListUserSwapOffers(ctx context.Context, userID string, filter SwapHistoryFilter) ([]SwapOffer, int, error) {
	baseQuery := `FROM swap_offers WHERE offeror_user_id = $1 OR offeree_user_id = $1`
	args := []any{userID}

	countQuery := `SELECT COUNT(*) ` + baseQuery
	var total int
	err := r.db.GetContext(ctx, &total, countQuery, args...)
	if err != nil {
		return nil, 0, err
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := filter.Offset

	query := `SELECT id, circle_id, offeror_user_id, offeree_user_id,
	                 offeror_asset, offeror_amount, requested_asset, requested_amount,
	                 status, transaction_hash, expires_at, created_at, updated_at ` +
		baseQuery + ` ORDER BY created_at DESC LIMIT $2 OFFSET $3`

	var offers []SwapOffer
	err = r.db.SelectContext(ctx, &offers, query, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	return offers, total, nil
}

func (r *PostgresRepository) ListCircleSwapOffers(ctx context.Context, circleID string, filter SwapHistoryFilter) ([]SwapOffer, int, error) {
	baseQuery := `FROM swap_offers WHERE circle_id = $1`
	args := []any{circleID}

	countQuery := `SELECT COUNT(*) ` + baseQuery
	var total int
	err := r.db.GetContext(ctx, &total, countQuery, args...)
	if err != nil {
		return nil, 0, err
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := filter.Offset

	query := `SELECT id, circle_id, offeror_user_id, offeree_user_id,
	                 offeror_asset, offeror_amount, requested_asset, requested_amount,
	                 status, transaction_hash, expires_at, created_at, updated_at ` +
		baseQuery + ` ORDER BY created_at DESC LIMIT $2 OFFSET $3`

	var offers []SwapOffer
	err = r.db.SelectContext(ctx, &offers, query, circleID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	return offers, total, nil
}

func (r *PostgresRepository) ListExpiredCreatedOffers(ctx context.Context, now time.Time) ([]SwapOffer, error) {
	query := `
		SELECT id, circle_id, offeror_user_id, offeree_user_id,
		       offeror_asset, offeror_amount, requested_asset, requested_amount,
		       status, transaction_hash, expires_at, created_at, updated_at
		FROM swap_offers
		WHERE status = 'created' AND expires_at <= $1
	`
	var offers []SwapOffer
	err := r.db.SelectContext(ctx, &offers, query, now)
	if err != nil {
		return nil, err
	}
	return offers, nil
}
