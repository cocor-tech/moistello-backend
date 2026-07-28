package swap

import (
	"context"
)

type Repository interface {
	CreateSwapOffer(ctx context.Context, offer *SwapOffer) error
	GetSwapOfferByID(ctx context.Context, id string) (*SwapOffer, error)
	UpdateSwapOfferStatus(ctx context.Context, id string, status SwapOfferStatus, transactionHash *string) error
	ListUserSwapOffers(ctx context.Context, userID string, filter SwapHistoryFilter) ([]SwapOffer, int, error)
	ListCircleSwapOffers(ctx context.Context, circleID string, filter SwapHistoryFilter) ([]SwapOffer, int, error)
	CancelExpiredOffers(ctx context.Context) error
}