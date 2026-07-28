package swap

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/moistello/backend/pkg/postgres"
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
			id, circle_id, offeror_user_id, offeree_user_id, offeror_asset, offeror_amount,
			requested_asset, requested_amount, status, expires_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	offer.CreatedAt = time.Now()
	offer.UpdatedAt = time.Now()
	offer.Status = SwapOfferStatusCreated

	_, err := r.db.ExecContext(ctx, query,
		offer.ID, offer.CircleID, offer.OfferorUserID, offer.OffereeUserID,
		offer.OfferorAsset, offer.OfferorAmount, offer.RequestedAsset, offer.RequestedAmount,
		offer.Status, offer.ExpiresAt, offer.CreatedAt, offer.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create swap offer: %w", postgres.HandleError(err))
	}

	return nil
}

func (r *PostgresRepository) GetSwapOfferByID(ctx context.Context, id string) (*SwapOffer, error) {
	query := `SELECT * FROM swap_offers WHERE id = $1`

	var offer SwapOffer
	err := r.db.GetContext(ctx, &offer, query, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get swap offer: %w", postgres.HandleError(err))
	}

	// Check if offer is expired
	if time.Now().After(offer.ExpiresAt) && offer.Status == SwapOfferStatusCreated {
		_ = r.UpdateSwapOfferStatus(ctx, id, SwapOfferStatusExpired, nil)
		offer.Status = SwapOfferStatusExpired
	}

	return &offer, nil
}

func (r *PostgresRepository) UpdateSwapOfferStatus(ctx context.Context, id string, status SwapOfferStatus, transactionHash *string) error {
	query := `UPDATE swap_offers SET status = $1, updated_at = $2`
	params := []interface{}{status, time.Now()}

	if transactionHash != nil {
		query += ", transaction_hash = $3"
		params = append(params, *transactionHash)
	}

	query += " WHERE id = $" + fmt.Sprintf("%d", len(params)+1)
	params = append(params, id)

	_, err := r.db.ExecContext(ctx, query, params...)
	if err != nil {
		return fmt.Errorf("failed to update swap offer status: %w", postgres.HandleError(err))
	}

	return nil
}

func (r *PostgresRepository) ListUserSwapOffers(ctx context.Context, userID string, filter SwapHistoryFilter) ([]SwapOffer, int, int, error) {
	baseQuery := `FROM swap_offers WHERE (offeror_user_id = $1 OR offeree_user_id = $1)`
	countQuery := `SELECT COUNT(*) ` + baseQuery
	query := `SELECT * ` + baseQuery

	args := []interface{}{userID}
	argIdx := 2

	if filter.CircleID != nil {
		baseQuery += fmt.Sprintf(" AND circle_id = $%d", argIdx)
		args = append(args, *filter.CircleID)
		argIdx++
	}

	if filter.Status != nil {
		baseQuery += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, *filter.Status)
		argIdx++
	}

	// Add pagination
	query = baseQuery + ` ORDER BY created_at DESC LIMIT $` + fmt.Sprintf("%d", argIdx) + ` OFFSET $` + fmt.Sprintf("%d", argIdx+1)
	args = append(args, filter.Limit, filter.Offset)

	var total int
	err := r.db.GetContext(ctx, &total, countQuery, userID)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to count swap offers: %w", err)
	}

	var offers []SwapOffer
	err = r.db.SelectContext(ctx, &offers, query, args...)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to list swap offers: %w", err)
	}

	return offers, total, len(offers), nil
}

func (r *PostgresRepository) ListCircleSwapOffers(ctx context.Context, circleID string, filter SwapHistoryFilter) ([]SwapOffer, int, error) {
	baseQuery := `FROM swap_offers WHERE circle_id = $1`
	countQuery := `SELECT COUNT(*) ` + baseQuery
	query := `SELECT * ` + baseQuery

	args := []interface{}{circleID}
	argIdx := 2

	if filter.Status != nil {
		baseQuery += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, *filter.Status)
		argIdx++
	}

	query = baseQuery + ` ORDER BY created_at DESC LIMIT $` + fmt.Sprintf("%d", argIdx) + ` OFFSET $` + fmt.Sprintf("%d", argIdx+1)
	args = append(args, filter.Limit, filter.Offset)

	var total int
	err := r.db.GetContext(ctx, &total, countQuery, circleID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count swap offers: %w", err)
	}

	var offers []SwapOffer
	err = r.db.SelectContext(ctx, &offers, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list swap offers: %w", err)
	}

	return offers, total, nil
}

func (r *PostgresRepository) CancelExpiredOffers(ctx context.Context) error {
	query := `UPDATE swap_offers SET status = $1, updated_at = $2 WHERE status = $3 AND expires_at < $4`

	_, err := r.db.ExecContext(ctx, query, SwapOfferStatusExpired, time.Now(), SwapOfferStatusCreated, time.Now())
	if err != nil {
		return fmt.Errorf("failed to cancel expired offers: %w", err)
	}

	return nil
}