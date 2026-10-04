package swap

import (
	"context"
	"time"
)

type Repository interface {
	CreateSwapOffer(ctx context.Context, offer *SwapOffer) error
	GetSwapOfferByID(ctx context.Context, id string) (*SwapOffer, error)
	UpdateSwapOfferStatus(ctx context.Context, id string, status SwapOfferStatus, transactionHash *string) error
	CompareAndSwapStatus(ctx context.Context, id string, expectedStatus, newStatus SwapOfferStatus, transactionHash *string) (bool, error)
	ListUserSwapOffers(ctx context.Context, userID string, filter SwapHistoryFilter) ([]SwapOffer, int, error)
	ListCircleSwapOffers(ctx context.Context, circleID string, filter SwapHistoryFilter) ([]SwapOffer, int, error)
	ListExpiredCreatedOffers(ctx context.Context, now time.Time) ([]SwapOffer, error)

	// ClaimOfferForSweep atomically takes ownership of one expired offer for
	// escrow release (#416). It transitions created -> sweeping and reports
	// whether this caller won the claim.
	//
	// This is the claim-then-process step that makes the sweep safe even when
	// the single-flight lock is unavailable or has expired: a caller that does
	// not win the claim never touches escrow for that offer. The lock stops
	// duplicate *work*; this stops duplicate *money movement*.
	ClaimOfferForSweep(ctx context.Context, id string, now time.Time) (bool, error)

	// ReleaseSweepClaim returns a claimed offer to the created state so a
	// later tick can retry it, and FinalizeSweep marks it expired. Both are
	// conditional on the offer still being in the sweeping state, so a stale
	// holder cannot clobber a concurrent transition.
	ReleaseSweepClaim(ctx context.Context, id string) error
	FinalizeSweep(ctx context.Context, id string, transactionHash *string) error
}
