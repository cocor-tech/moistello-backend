package withdrawal

import "time"

type WithdrawalStatus string

const (
	WithdrawalStatusPending    WithdrawalStatus = "pending"       // User needs to send USDC
	WithdrawalStatusReceived   WithdrawalStatus = "received"      // USDC received by platform
	WithdrawalStatusConverting WithdrawalStatus = "converting"    // Converting to NGN via Yellow Card
	WithdrawalStatusProcessing WithdrawalStatus = "processing"    // Yellow Card processing bank transfer
	WithdrawalStatusCompleted  WithdrawalStatus = "completed"     // Bank transfer completed
	WithdrawalStatusFailed     WithdrawalStatus = "failed"        // Failed at any stage
)

type Withdrawal struct {
	ID              string          `json:"id" db:"id"`
	UserID          string          `json:"userId" db:"user_id"`
	AmountUSDC      int64           `json:"amountUsdc" db:"amount_usdc"`
	EstimatedNGN    int64           `json:"estimatedNgn" db:"estimated_ngn"`
	BankCode        string          `json:"bankCode" db:"bank_code"`
	AccountNumber   string          `json:"accountNumber" db:"account_number"`
	AccountName     string          `json:"accountName" db:"account_name"`
	Status          WithdrawalStatus `json:"status" db:"status"`
	
	// Platform receiving address (where user sends USDC)
	PlatformAddress string          `json:"platformAddress" db:"platform_address"`
	
	// Transaction hashes
	USDCtxHash      *string         `json:"usdcTxHash,omitempty" db:"usdc_tx_hash"`
	YellowCardTxID  *string         `json:"yellowCardTxId,omitempty" db:"yellow_card_tx_id"`
	
	// Timestamps
	CreatedAt       time.Time       `json:"createdAt" db:"created_at"`
	ReceivedAt      *time.Time      `json:"receivedAt,omitempty" db:"received_at"`
	CompletedAt     *time.Time      `json:"completedAt,omitempty" db:"completed_at"`
	
	// Error handling
	FailureReason   *string         `json:"failureReason,omitempty" db:"failure_reason"`
	
	// Yellow Card details
	PaymentRef      string          `json:"paymentRef" db:"payment_ref"`
}

type WithdrawalRequest struct {
	AmountUSDC    int64  `json:"amountUsdc" binding:"required,gt=0"`
	BankCode      string `json:"bankCode" binding:"required"`
	AccountNumber string `json:"accountNumber" binding:"required"`
	AccountName   string `json:"accountName" binding:"required"`
}
