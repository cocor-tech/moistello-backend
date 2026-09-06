package contribution

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	FindByID(ctx context.Context, id uuid.UUID) (*Contribution, error)
	FindByCircleAndUser(ctx context.Context, circleID, userID uuid.UUID) (*Contribution, error)
	// FindByTxnHash returns the existing contribution for a given on-chain
	// transaction hash. Used for idempotent replay detection.
	FindByTxnHash(ctx context.Context, txnHash string) (*Contribution, error)
	Create(ctx context.Context, c *Contribution) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status ContributionStatus, txnHash string) error
	UpdateVerificationStatus(ctx context.Context, id uuid.UUID, verifiedOnchain bool, status VerificationStatus) error
	ListByUser(ctx context.Context, userID uuid.UUID, page, limit int) ([]Contribution, int, error)
	ListByCircle(ctx context.Context, circleID uuid.UUID, page, limit int) ([]Contribution, int, error)
}
