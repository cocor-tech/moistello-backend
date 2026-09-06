package payout

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	FindByID(ctx context.Context, id uuid.UUID) (*Payout, error)
	// FindByTxnHash returns the existing payout for a given on-chain
	// transaction hash. Used for idempotent replay detection.
	FindByTxnHash(ctx context.Context, txnHash string) (*Payout, error)
	Create(ctx context.Context, p *Payout) error
	UpdateVerificationStatus(ctx context.Context, id uuid.UUID, verifiedOnchain bool, status VerificationStatus) error
	ListByUser(ctx context.Context, userID uuid.UUID, page, limit int) ([]Payout, int, error)
	ListByCircle(ctx context.Context, circleID uuid.UUID, page, limit int) ([]Payout, int, error)
}
