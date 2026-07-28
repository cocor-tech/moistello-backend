package treasury

import "time"

type WithdrawalStatus string

const (
	WithdrawalStatusPending   WithdrawalStatus = "pending"
	WithdrawalStatusApproved  WithdrawalStatus = "approved"
	WithdrawalStatusRejected  WithdrawalStatus = "rejected"
	WithdrawalStatusCompleted WithdrawalStatus = "completed"
	WithdrawalStatusFailed    WithdrawalStatus = "failed"
)

type ApprovalMethod string

const (
	ApprovalMethodMultiSig ApprovalMethod = "multisig"
	ApprovalMethodTimelock ApprovalMethod = "timelock"
)

type TreasuryBalance struct {
	XLM  string `json:"xlm"`
	USDC string `json:"usdc"`
}

type WithdrawalRequest struct {
	ID              string           `json:"id" db:"id"`
	Destination     string           `json:"destination" db:"destination"`
	Asset           string           `json:"asset" db:"asset"`
	Amount          int64            `json:"amount" db:"amount"`
	Status          WithdrawalStatus `json:"status" db:"status"`
	ApprovalMethod  ApprovalMethod   `json:"approvalMethod" db:"approval_method"`
	RequestedBy     string           `json:"requestedBy" db:"requested_by"`
	RequestedAt     time.Time        `json:"requestedAt" db:"requested_at"`
	ApprovedBy      *string          `json:"approvedBy,omitempty" db:"approved_by"`
	ApprovedAt      *time.Time       `json:"approvedAt,omitempty" db:"approved_at"`
	TxHash          *string          `json:"txHash,omitempty" db:"tx_hash"`
	FailureReason   *string          `json:"failureReason,omitempty" db:"failure_reason"`
	ExecutedAt      *time.Time       `json:"executedAt,omitempty" db:"executed_at"`
	TimelockExpiry  *time.Time       `json:"timelockExpiry,omitempty" db:"timelock_expiry"`
	MultiSigSignatures []string     `json:"multiSigSignatures,omitempty" db:"multi_sig_signatures"`
}

type TreasuryTransaction struct {
	ID          string    `json:"id" db:"id"`
	Type        string    `json:"type" db:"type"` // "deposit", "withdrawal", "fee", "payout"
	Asset       string    `json:"asset" db:"asset"`
	Amount      int64     `json:"amount" db:"amount"`
	From        *string   `json:"from,omitempty" db:"from_address"`
	To          *string   `json:"to,omitempty" db:"to_address"`
	TxHash      string    `json:"txHash" db:"tx_hash"`
	Description *string   `json:"description,omitempty" db:"description"`
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
}
