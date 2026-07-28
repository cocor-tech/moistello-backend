package swap

import (
	"time"
)

type SwapOfferStatus string

const (
	SwapOfferStatusCreated   SwapOfferStatus = "created"
	SwapOfferStatusAccepted  SwapOfferStatus = "accepted"
	SwapOfferStatusCompleted SwapOfferStatus = "completed"
	SwapOfferStatusCancelled SwapOfferStatus = "cancelled"
	SwapOfferStatusExpired   SwapOfferStatus = "expired"
)

type SwapOffer struct {
	ID                string           `json:"id" db:"id"`
	CircleID          string           `json:"circleId" db:"circle_id"`
	OfferorUserID     string           `json:"offerorUserId" db:"offeror_user_id"`
	OffereeUserID     *string          `json:"offereeUserId,omitempty" db:"offeree_user_id"`
	OfferorAsset      string           `json:"offerorAsset" db:"offeror_asset"`
	OfferorAmount     int64            `json:"offerorAmount" db:"offeror_amount"`
	RequestedAsset    string           `json:"requestedAsset" db:"requested_asset"`
	RequestedAmount   int64            `json:"requestedAmount" db:"requested_amount"`
	Status            SwapOfferStatus  `json:"status" db:"status"`
	TransactionHash   *string          `json:"transactionHash,omitempty" db:"transaction_hash"`
	ExpiresAt         time.Time        `json:"expiresAt" db:"expires_at"`
	CreatedAt         time.Time        `json:"createdAt" db:"created_at"`
	UpdatedAt         time.Time        `json:"updatedAt" db:"updated_at"`
}

type SwapOfferRequest struct {
	CircleID        string `json:"circleId" binding:"required"`
	OffereeUserID   *string `json:"offereeUserId,omitempty"`
	OfferorAsset    string `json:"offerorAsset" binding:"required"`
	OfferorAmount   int64  `json:"offerorAmount" binding:"required,min=1"`
	RequestedAsset  string `json:"requestedAsset" binding:"required"`
	RequestedAmount int64  `json:"requestedAmount" binding:"required,min=1"`
	ExpiresIn       int    `json:"expiresIn" binding:"min=1,max=168"` // hours, max 7 days
}

type SwapAcceptRequest struct {
	SwapOfferID string `json:"swapOfferId" binding:"required"`
}

type SwapHistoryFilter struct {
	CircleID *string `json:"circleId,omitempty"`
	Status   *string `json:"status,omitempty"`
	Limit    int     `json:"limit" binding:"min=1,max=100"`
	Offset   int     `json:"offset" binding:"min=0"`
}

type SwapHistoryResponse struct {
	Swaps      []SwapOffer `json:"swaps"`
	Total      int         `json:"total"`
	Limit      int         `json:"limit"`
	Offset     int         `json:"offset"`
}