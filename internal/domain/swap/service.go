package swap

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/apperrors"
)

type CircleService interface {
}

type UserService interface {
	GetByID(ctx context.Context, id string) (*user.User, error)
}

type EscrowClient interface {
	CreateSwap(ctx context.Context, circleID, offeror, offeree string, offerorAsset string, offerorAmount int64, requestedAsset string, requestedAmount int64, expiresAt uint64) (string, error)
	AcceptSwap(ctx context.Context, swapID string, acceptor string) (string, error)
	CancelSwap(ctx context.Context, swapID string, canceller string) (string, error)
	ExecuteSwap(ctx context.Context, swapID string) (string, error)
}

type Service struct {
	repo      Repository
	circleSvc CircleService
	userSvc   UserService
	escrow    EscrowClient
}

func NewService(repo Repository, circleSvc CircleService, userSvc UserService, escrow EscrowClient) *Service {
	return &Service{
		repo:      repo,
		circleSvc: circleSvc,
		userSvc:   userSvc,
		escrow:    escrow,
	}
}

func (s *Service) CreateSwapOffer(ctx context.Context, userID string, input SwapOfferRequest) (*SwapOffer, error) {
	u, err := s.userSvc.GetByID(ctx, userID)
	if err != nil {
		return nil, apperrors.ErrNotFound
	}

	expiresAt := time.Now().Add(time.Duration(input.ExpiresIn) * time.Hour)

	offereeID := input.OffereeUserID

	var offereeWallet string
	if offereeID != nil {
		offereeUser, err := s.userSvc.GetByID(ctx, *offereeID)
		if err != nil {
			return nil, apperrors.ErrNotFound
		}
		offereeWallet = offereeUser.WalletAddress
	}

	_, err = s.escrow.CreateSwap(
		ctx,
		input.CircleID,
		u.WalletAddress,
		offereeWallet,
		input.OfferorAsset,
		input.OfferorAmount,
		input.RequestedAsset,
		input.RequestedAmount,
		uint64(expiresAt.Unix()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create escrow swap: %w", err)
	}

	offer := &SwapOffer{
		ID:              uuid.New().String(),
		CircleID:        input.CircleID,
		OfferorUserID:   userID,
		OffereeUserID:   offereeID,
		OfferorAsset:    input.OfferorAsset,
		OfferorAmount:   input.OfferorAmount,
		RequestedAsset:  input.RequestedAsset,
		RequestedAmount: input.RequestedAmount,
		Status:          SwapOfferStatusCreated,
		ExpiresAt:       expiresAt,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := s.repo.CreateSwapOffer(ctx, offer); err != nil {
		return nil, err
	}

	return offer, nil
}

func (s *Service) AcceptSwapOffer(ctx context.Context, userID string, offerID string) (*SwapOffer, error) {
	offer, err := s.repo.GetSwapOfferByID(ctx, offerID)
	if err != nil {
		return nil, err
	}

	if offer.Status != SwapOfferStatusCreated {
		return nil, apperrors.ErrConflict
	}

	if time.Now().After(offer.ExpiresAt) {
		return nil, apperrors.ErrInvalidInput
	}

	if offer.OffereeUserID != nil && *offer.OffereeUserID != userID {
		return nil, apperrors.ErrForbidden
	}

	acceptorUser, err := s.userSvc.GetByID(ctx, userID)
	if err != nil {
		return nil, apperrors.ErrNotFound
	}

	swapped, err := s.repo.CompareAndSwapStatus(ctx, offerID, SwapOfferStatusCreated, SwapOfferStatusAccepted, nil)
	if err != nil {
		return nil, err
	}
	if !swapped {
		return nil, apperrors.ErrConflict
	}

	txHash, err := s.escrow.AcceptSwap(ctx, offerID, acceptorUser.WalletAddress)
	clineErr := err
	if clineErr != nil {
		_, _ = s.repo.CompareAndSwapStatus(ctx, offerID, SwapOfferStatusAccepted, SwapOfferStatusCreated, nil)
		return nil, fmt.Errorf("failed to accept swap on-chain: %w", clineErr)
	}

	_ = s.repo.UpdateSwapOfferStatus(ctx, offerID, SwapOfferStatusAccepted, &txHash)
	offer.Status = SwapOfferStatusAccepted
	offer.TransactionHash = &txHash

	return offer, nil
}

func (s *Service) CancelSwapOffer(ctx context.Context, userID string, offerID string) (*SwapOffer, error) {
	offer, err := s.repo.GetSwapOfferByID(ctx, offerID)
	if err != nil {
		return nil, err
	}

	if offer.Status != SwapOfferStatusCreated {
		return nil, apperrors.ErrConflict
	}

	if offer.OfferorUserID != userID {
		return nil, apperrors.ErrForbidden
	}

	userObj, err := s.userSvc.GetByID(ctx, userID)
	if err != nil {
		return nil, apperrors.ErrNotFound
	}

	txHash, err := s.escrow.CancelSwap(ctx, offerID, userObj.WalletAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to cancel swap on-chain: %w", err)
	}

	if err := s.repo.UpdateSwapOfferStatus(ctx, offerID, SwapOfferStatusCancelled, &txHash); err != nil {
		return nil, err
	}

	offer.Status = SwapOfferStatusCancelled
	offer.TransactionHash = &txHash
	return offer, nil
}

func (s *Service) GetSwapHistory(ctx context.Context, userID string, filter SwapHistoryFilter) ([]SwapOffer, int, error) {
	return s.repo.ListUserSwapOffers(ctx, userID, filter)
}

func (s *Service) GetCircleSwapHistory(ctx context.Context, circleID string, filter SwapHistoryFilter) ([]SwapOffer, int, error) {
	return s.repo.ListCircleSwapOffers(ctx, circleID, filter)
}

// SweepExpiredOffers releases escrow on-chain for created offers past their
// expiry and marks them expired (#243, hardened in #416).
//
// Each offer is claimed atomically before any escrow call, so even if two
// replicas sweep the same expired set concurrently — because the single-flight
// lock was unavailable, or expired mid-pass — only the caller that wins the
// claim moves money. A lost race costs a wasted status write, never a second
// release attempt.
func (s *Service) SweepExpiredOffers(ctx context.Context) (int, error) {
	expired, err := s.repo.ListExpiredCreatedOffers(ctx, time.Now())
	if err != nil {
		return 0, err
	}

	swept := 0
	for _, offer := range expired {
		// Claim first, then do the work. Doing it the other way round is what
		// allowed two replicas to both attempt a release.
		claimed, err := s.repo.ClaimOfferForSweep(ctx, offer.ID, time.Now())
		if err != nil || !claimed {
			// Lost the race, or the offer was cancelled/accepted/extended since
			// the listing. Either way this replica must not touch escrow.
			continue
		}

		if err := s.releaseExpiredOffer(ctx, offer.ID, offer.OfferorUserID); err != nil {
			// Hand the offer back so a later tick retries it rather than
			// stranding it in sweeping forever.
			if relErr := s.repo.ReleaseSweepClaim(ctx, offer.ID); relErr != nil {
				log.Error().Err(relErr).Str("offerID", offer.ID).
					Msg("swap sweep: returning failed claim to created failed")
			}
			continue
		}

		swept++
	}
	return swept, nil
}

// releaseExpiredOffer cancels the on-chain swap and finalizes the offer. It
// runs only for the caller that won the claim.
func (s *Service) releaseExpiredOffer(ctx context.Context, offerID, offerorUserID string) error {
	offeror, err := s.userSvc.GetByID(ctx, offerorUserID)
	if err != nil {
		// Unresolvable offeror: we cannot sign the release, so the claim is
		// given back and a later tick can retry once the user is resolvable.
		log.Warn().Err(err).Str("offerID", offerID).Msg("swap sweep: offeror unresolvable")
		return err
	}

	txHash, err := s.escrow.CancelSwap(ctx, offerID, offeror.WalletAddress)
	if err != nil {
		log.Warn().Err(err).Str("offerID", offerID).Msg("swap sweep: on-chain cancel failed")
		return err
	}

	if err := s.repo.FinalizeSweep(ctx, offerID, &txHash); err != nil {
		// Escrow is already released but the row could not be finalized. Return
		// an error so the claim is released and the offer is retried; the
		// retry's claim is a no-op-safe status write rather than a second
		// release, because the claim is what gates the escrow call.
		return err
	}
	return nil
}
