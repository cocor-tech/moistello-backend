package payout

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type PayoutType string

const (
	PayoutTypeRandom  PayoutType = "random"
	PayoutTypeFixed   PayoutType = "fixed"
	PayoutTypeAuction PayoutType = "auction"
	PayoutTypeVote    PayoutType = "vote"
)

type VerificationStatus string

const (
	VerificationStatusUnverified VerificationStatus = "unverified"
	VerificationStatusPending    VerificationStatus = "pending"
	VerificationStatusVerified   VerificationStatus = "verified"
	VerificationStatusFailed     VerificationStatus = "failed"
)

type Payout struct {
	ID                 uuid.UUID          `json:"id" db:"id"`
	CircleID           uuid.UUID          `json:"circleId" db:"circle_id"`
	RecipientID        uuid.UUID          `json:"recipientId" db:"recipient_id"`
	RoundNumber        int                `json:"roundNumber" db:"round_number"`
	Amount             float64            `json:"amount" db:"amount"`
	FeeAmount          float64            `json:"feeAmount" db:"fee_amount"`
	TxnHash            sql.NullString     `json:"txnHash,omitempty" db:"txn_hash"`
	PayoutType         PayoutType         `json:"payoutType" db:"payout_type"`
	VerifiedOnchain    bool               `json:"verifiedOnchain" db:"verified_onchain"`
	VerificationStatus VerificationStatus `json:"verificationStatus" db:"verification_status"`
	CreatedAt          time.Time          `json:"createdAt" db:"created_at"`
	UpdatedAt          time.Time          `json:"updatedAt" db:"updated_at"`
	OnTime             bool               `json:"onTime" db:"on_time"`
}
