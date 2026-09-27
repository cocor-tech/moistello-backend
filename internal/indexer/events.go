package indexer

// Contract event type constants — must match the Symbol topics emitted by the
// Soroban contracts (CircleFactory, Circle, ReputationRegistry, Treasury).
const (
	EventCircleCreated        = "CircleCreated"
	EventMemberJoined         = "MemberJoined"
	EventContributionReceived = "ContributionReceived"
	EventPayoutExecuted       = "PayoutExecuted"
	EventLateReported         = "LateReported"
	EventMemberExited         = "MemberExited"
	EventDefaultRecorded      = "DefaultRecorded"
	EventCircleCompleted      = "CircleCompleted"
	EventAuctionBid           = "AuctionBid"
	EventVoteCast             = "VoteCast"
	EventDisputeRaised        = "DisputeRaised"
	EventFeeDeposited         = "FeeDeposited"
)

// ContractEvent is a fully-decoded Soroban contract event extracted from a
// Stellar transaction's result_meta_xdr. All 12 event types share this struct;
// the typed payload structs below are used only within handler implementations.
type ContractEvent struct {
	// ContractID is the Soroban contract address that emitted the event.
	ContractID string `json:"contract_id"`
	// EventType corresponds to one of the EventXxx constants above.
	EventType string `json:"event_type"`
	// Ledger is the Stellar ledger sequence number containing this event.
	Ledger int64 `json:"ledger"`
	// TxHash is the transaction hash that produced this event.
	TxHash string `json:"tx_hash"`
	// ContractVersion is the executable (WASM) hash of the contract that emitted
	// the event, recorded so an event stays attributable to the exact code that
	// produced it across contract upgrades, which leave the contract ID
	// unchanged. It is ContractVersionUnknown when the deployed version could
	// not be resolved.
	//
	// Decoding leaves this empty: the version is not carried in the event XDR
	// and is filled in by the processor when the event is recorded, so it is
	// omitted from a decoded event's JSON rather than rendered as a misleading
	// empty version.
	ContractVersion string `json:"contract_version,omitempty"`
	// Payload is a flat map of decoded XDR field names → Go-native values.
	// Keys and value types match the typed payload structs below.
	Payload map[string]any `json:"payload"`
}

// ---------------------------------------------------------------------------
// Typed payload structs — one per event type.
// These are used inside handler functions to safely extract Payload fields.
// ---------------------------------------------------------------------------

// CircleCreatedPayload corresponds to CircleCreated(circle_id, creator, config_hash).
type CircleCreatedPayload struct {
	CircleID   string `json:"circle_id"`
	Creator    string `json:"creator"`
	ConfigHash string `json:"config_hash"`
}

// MemberJoinedPayload corresponds to MemberJoined(circle_id, member, contribution_amount).
type MemberJoinedPayload struct {
	CircleID           string  `json:"circle_id"`
	Member             string  `json:"member"`
	ContributionAmount float64 `json:"contribution_amount"`
}

// ContributionReceivedPayload corresponds to ContributionReceived(circle_id, member, amount, round).
type ContributionReceivedPayload struct {
	CircleID string  `json:"circle_id"`
	Member   string  `json:"member"`
	Amount   float64 `json:"amount"`
	Round    int     `json:"round"`
}

// PayoutExecutedPayload corresponds to PayoutExecuted(circle_id, recipient, amount, round, payout_type).
type PayoutExecutedPayload struct {
	CircleID   string  `json:"circle_id"`
	Recipient  string  `json:"recipient"`
	Amount     float64 `json:"amount"`
	Round      int     `json:"round"`
	PayoutType string  `json:"payout_type"`
}

// LateReportedPayload corresponds to LateReported(circle_id, member, penalty_amount, strikes).
type LateReportedPayload struct {
	CircleID      string  `json:"circle_id"`
	Member        string  `json:"member"`
	PenaltyAmount float64 `json:"penalty_amount"`
	Strikes       int     `json:"strikes"`
}

// MemberExitedPayload corresponds to MemberExited(circle_id, member, penalty).
type MemberExitedPayload struct {
	CircleID string  `json:"circle_id"`
	Member   string  `json:"member"`
	Penalty  float64 `json:"penalty"`
}

// DefaultRecordedPayload corresponds to DefaultRecorded(circle_id, member).
type DefaultRecordedPayload struct {
	CircleID string `json:"circle_id"`
	Member   string `json:"member"`
}

// CircleCompletedPayload corresponds to CircleCompleted(circle_id, total_contributions).
type CircleCompletedPayload struct {
	CircleID           string  `json:"circle_id"`
	TotalContributions float64 `json:"total_contributions"`
}

// AuctionBidPayload corresponds to AuctionBid(circle_id, bidder, discount_bips, round).
type AuctionBidPayload struct {
	CircleID     string `json:"circle_id"`
	Bidder       string `json:"bidder"`
	DiscountBips int    `json:"discount_bips"`
	Round        int    `json:"round"`
}

// VoteCastPayload corresponds to VoteCast(circle_id, voter, vote_for, round).
type VoteCastPayload struct {
	CircleID string `json:"circle_id"`
	Voter    string `json:"voter"`
	VoteFor  string `json:"vote_for"`
	Round    int    `json:"round"`
}

// DisputeRaisedPayload corresponds to DisputeRaised(circle_id, member, evidence_hash).
type DisputeRaisedPayload struct {
	CircleID     string `json:"circle_id"`
	Member       string `json:"member"`
	EvidenceHash string `json:"evidence_hash"`
}

// FeeDepositedPayload corresponds to the Treasury contract's FeeDeposited event.
type FeeDepositedPayload struct {
	CircleID string  `json:"circle_id"`
	Amount   float64 `json:"amount"`
	TxHash   string  `json:"tx_hash"`
}
