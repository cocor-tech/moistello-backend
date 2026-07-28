package swap

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/pkg/stellar/soroban"
	"github.com/moistello/backend/pkg/apperrors"
)

type Service struct {
	repo           Repository
	circleService  *circle.Service
	userService    *user.Service
	escrowSwapClient *soroban.EscrowSwapClient
}

func NewService(
	repo Repository,
	circleService *circle.Service,
	userService *user.Service,
	escrowSwapClient *soroban.EscrowSwapClient,
) *Service {
	return &Service{
		repo:           repo,
		circleService:  circleService,
		userService:    userService,
		escrowSwapClient: escrowSwapClient,
	}
}

func (s *Service) CreateSwapOffer(ctx context.Context, userID string, req SwapOfferRequest) (*SwapOffer, error) {
	// Verify user is a member of the circle
	_, err := s.circleService.GetCircle(ctx, req.CircleID)
	if err != nil {
		return nil, apperrors.NewBadRequest("invalid circle ID")
	}

	isMember, err := s.circleService.IsMember(ctx, req.CircleID, userID)
	if err != nil || !isMember {
		return nil, apperrors.NewForbidden("user is not a member of this circle")
	}

	// If offeree is specified, verify they are also a circle member
	if req.OffereeUserID != nil {
		isOffereeMember, err := s.circleService.IsMember(ctx, req.CircleID, *req.OffereeUserID)
		if err != nil || !isOffereeMember {
			return nil, apperrors.NewBadRequest("offeree is not a member of this circle")
		}
	}

	// Create swap offer in database
	offer := &SwapOffer{
		ID:             uuid.NewString(),
		CircleID:       req.CircleID,
		OfferorUserID:  userID,
		OffereeUserID:  req.OffereeUserID,
		OfferorAsset:   req.OfferorAsset,
		OfferorAmount:  req.OfferorAmount,
		RequestedAsset: req.RequestedAsset,
		RequestedAmount: req.RequestedAmount,
		ExpiresAt:      time.Now().Add(time.Duration(req.ExpiresIn) * time.Hour),
	}

	err = s.repo.CreateSwapOffer(ctx, offer)
	if err != nil {
		return nil, fmt.Errorf("failed to create swap offer: %w", err)
	}

	// Create swap on the escrow contract
	expiresAtUnix := uint64(offer.ExpiresAt.Unix())
	contractUserID, err := s.getUserContractID(ctx, userID)
	if err != nil {
		return nil, err
	}

	var contractOffereeID string
	if req.OffereeUserID != nil {
		contractOffereeID, err = s.getUserContractID(ctx, *req.OffereeUserID)
		if err != nil {
			return nil, err
		}
	}

	txHash, err := s.escrowSwapClient.CreateSwap(
		ctx,
		req.CircleID,
		contractUserID,
		contractOffereeID,
		req.OfferorAsset,
		req.OfferorAmount,
		req.RequestedAsset,
		req.RequestedAmount,
		expiresAtUnix,
	)
	if err != nil {
		// Mark offer as failed if contract creation fails
		_ = s.repo.UpdateSwapOfferStatus(ctx, offer.ID, SwapOfferStatusCancelled, nil)
		return nil, fmt.Errorf("failed to create swap on chain: %w", err)
	}

	// Update with transaction hash
	offer.TransactionHash = &txHash
	err = s.repo.UpdateSwapOfferStatus(ctx, offer.ID, offer.Status, &txHash)
	if err != nil {
		return offer, nil // Still return the offer even if DB update fails
	}

	return offer, nil
}

func (s *Service) AcceptSwapOffer(ctx context.Context, userID string, swapOfferID string) (*SwapOffer, error) {
	// Get the swap offer
	offer, err := s.repo.GetSwapOfferByID(ctx, swapOfferID)
	if err != nil {
		return nil, apperrors.NewNotFound("swap offer not found")
	}

	// Verify the offer is in created status
	if offer.Status != SwapOfferStatusCreated {
		return nil, apperrors.NewBadRequest("swap offer is not available for acceptance")
	}

	// Verify the acceptor is the intended offeree (or any member if no offeree specified)
	if offer.OffereeUserID != nil && *offer.OffereeUserID != userID {
		return nil, apperrors.NewForbidden("only the specified offeree can accept this swap")
	}

	// Verify acceptor is a circle member
	isMember, err := s.circleService.IsMember(ctx, offer.CircleID, userID)
	if err != nil || !isMember {
		return nil, apperrors.NewForbidden("user is not a member of this circle")
	}

	// Verify the acceptor is not the offeror
	if offer.OfferorUserID == userID {
		return nil, apperrors.NewBadRequest("cannot accept your own swap offer")
	}

	// Update database first
	offer.OffereeUserID = &userID
	offer.Status = SwapOfferStatusAccepted
	err = s.repo.UpdateSwapOfferStatus(ctx, swapOfferID, SwapOfferStatusAccepted, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to update swap offer: %w", err)
	}

	// Accept swap on chain
	contractUserID, err := s.getUserContractID(ctx, userID)
	if err != nil {
		return nil, err
	}

	txHash, err := s.escrowSwapClient.AcceptSwap(ctx, swapOfferID, contractUserID)
	if err != nil {
		// Revert status if contract call fails
		_ = s.repo.UpdateSwapOfferStatus(ctx, swapOfferID, SwapOfferStatusCreated, nil)
		return nil, fmt.Errorf("failed to accept swap on chain: %w", err)
	}

	// Execute the swap automatically after acceptance (zero spread, atomic swap)
	executeTxHash, err := s.escrowSwapClient.ExecuteSwap(ctx, swapOfferID)
	if err != nil {
		// If execution fails, still mark as accepted but log the error
		offer.TransactionHash = &txHash
		return offer, fmt.Errorf("swap accepted but execution failed: %w", err)
	}

	// Mark as completed
	finalTxHash := executeTxHash
	offer.TransactionHash = &finalTxHash
	offer.Status = SwapOfferStatusCompleted
	err = s.repo.UpdateSwapOfferStatus(ctx, swapOfferID, SwapOfferStatusCompleted, &finalTxHash)
	if err != nil {
		return offer, nil
	}

	return offer, nil
}

func (s *Service) GetSwapHistory(ctx context.Context, userID string, filter SwapHistoryFilter) (*SwapHistoryResponse, error) {
	swaps, total, _, err := s.repo.ListUserSwapOffers(ctx, userID, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch swap history: %w", err)
	}

	return &SwapHistoryResponse{
		Swaps:  swaps,
		Total:  total,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	}, nil
}

func (s *Service) getUserContractID(ctx context.Context, userID string) (string, error) {
	user, err := s.userService.GetByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("failed to get user: %w", err)
	}
	// Return user's wallet public key as the contract identifier
	if len(user.Wallets) > 0 {
		return user.Wallets[0].PublicKey, nil
	}
	return "", apperrors.NewBadRequest("user has no wallet")
}