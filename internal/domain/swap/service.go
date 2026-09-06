package swap

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
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

func (s *Service) SweepExpiredOffers(ctx context.Context) (int, error) {
	expired, err := s.repo.ListExpiredCreatedOffers(ctx, time.Now())
	if err != nil {
		return 0, err
	}

	swept := 0
	for _, offer := range expired {
		offeror, err := s.userSvc.GetByID(ctx, offer.OfferorUserID)
		if err != nil {
			continue
		}

		swapped, err := s.repo.CompareAndSwapStatus(ctx, offer.ID, SwapOfferStatusCreated, SwapOfferStatusExpired, nil)
		if err != nil || !swapped {
			continue
		}

		_, err = s.escrow.CancelSwap(ctx, offer.ID, offeror.WalletAddress)
		if err != nil {
			_, _ = s.repo.CompareAndSwapStatus(ctx, offer.ID, SwapOfferStatusExpired, SwapOfferStatusCreated, nil)
			continue
		}

		swept++
	}
	return swept, nil
}
