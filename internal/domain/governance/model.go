package governance

import (
	"time"

	"github.com/google/uuid"
)

type ProposalStatus string

const (
	ProposalStatusPending  ProposalStatus = "pending"
	// ProposalStatusPassed means the vote carried and the proposal is waiting
	// out its execution timelock (#414). It was declared but never assigned
	// before #414; execution used to go straight from pending to executed.
	ProposalStatusPassed   ProposalStatus = "passed"
	ProposalStatusRejected ProposalStatus = "rejected"
	ProposalStatusExecuted ProposalStatus = "executed"
	// ProposalStatusCancelled means the proposal passed but was cancelled by
	// threshold vote inside its timelock window (#414).
	ProposalStatusCancelled ProposalStatus = "cancelled"
)

// TimelockRule documents the execution timelock and is returned on proposal
// reads so the rule is visible to API consumers rather than only to people
// reading the source (#414).
//
// The rule: when a proposal's vote passes it moves to 'passed' and becomes
// executable at ExecutableAt, which is the decision time plus the configured
// delay. It cannot be executed before then, and until it is executed a
// threshold of members can cancel it. Once ExecutableAt passes the cancellation
// window closes and the proposal can be executed.
const TimelockRule = "A proposal that passes its vote becomes executable at executableAt, which is the time the vote passed plus the configured execution delay. It cannot be executed before then. Until it is executed, a threshold of members may cancel it by voting to cancel; once executableAt has passed the cancellation window is closed."

type Proposal struct {
	ID           uuid.UUID      `json:"id" db:"id"`
	Title        string         `json:"title" db:"title"`
	Description  string         `json:"description" db:"description"`
	ProposalType string         `json:"proposalType" db:"proposal_type"`
	CreatorID    uuid.UUID      `json:"creatorId" db:"creator_id"`
	Status       ProposalStatus `json:"status" db:"status"`
	ForVotes     int            `json:"forVotes" db:"for_votes"`
	AgainstVotes int            `json:"againstVotes" db:"against_votes"`
	ExecutedAt   *time.Time     `json:"executedAt,omitempty" db:"executed_at"`
	CreatedAt    time.Time      `json:"createdAt" db:"created_at"`
	UpdatedAt    time.Time      `json:"updatedAt" db:"updated_at"`

	// ExecutableAt is when a passed proposal becomes executable (#414). It is
	// set when the vote passes, so the timelock runs from the decision rather
	// than from when voting opened. It is surfaced on reads so a client can show
	// a countdown and know when the cancellation window closes.
	ExecutableAt *time.Time `json:"executableAt,omitempty" db:"executable_at"`
	// CancelledAt is when the proposal was cancelled inside its timelock
	// window, or nil if it never was.
	CancelledAt *time.Time `json:"cancelledAt,omitempty" db:"cancelled_at"`
	// CancellationVotes is how many members have voted to cancel. It is
	// transient state for the timelock window, not part of the original tally.
	CancellationVotes int `json:"cancellationVotes" db:"-"`
}

// TimelockConfig is the configurable execution delay and cancellation
// threshold (#414).
type TimelockConfig struct {
	// Delay is how long a passed proposal waits before it can be executed. A
	// zero or negative delay disables the timelock, in which case a passed
	// proposal executes immediately — the behaviour before #414. That default
	// keeps the service usable without governance configuration; the deployment
	// config supplies the real value.
	Delay time.Duration
	// CancelThresholdPct is the share of votes cast (for + against) that must
	// vote to cancel in order to cancel a proposal inside its timelock window.
	// Values outside 1..100 fall back to DefaultCancelThresholdPct.
	CancelThresholdPct int
}

// DefaultCancelThresholdPct is the cancellation threshold used when none is
// configured. A third of voters is high enough that one member acting alone
// cannot cancel a proposal, and low enough that a genuine wave of regret can.
const DefaultCancelThresholdPct = 33

// Normalize clamps the cancellation threshold into 1..100.
func (c TimelockConfig) Normalize() TimelockConfig {
	if c.CancelThresholdPct < 1 || c.CancelThresholdPct > 100 {
		c.CancelThresholdPct = DefaultCancelThresholdPct
	}
	return c
}

// CancellationThreshold is the number of cancellation votes needed to cancel a
// proposal that received totalVotes votes, always at least 1.
//
// The threshold is a share of votes actually cast rather than of the electorate:
// a proposal nobody else voted on should not be uncancellable just because the
// community is large, and requiring a fixed absolute number would let a handful
// of whales cancel a broadly-supported proposal.
func CancellationThreshold(totalVotes, pct int) int {
	if totalVotes <= 0 {
		return 1
	}
	threshold := (totalVotes*pct + 99) / 100 // ceil without floats
	if threshold < 1 {
		return 1
	}
	return threshold
}

type CreateProposalInput struct {
	Title        string `json:"title" binding:"required"`
	Description  string `json:"description" binding:"required"`
	ProposalType string `json:"proposalType" binding:"required"`
	CreatorID    string `json:"creatorId" binding:"required"`
}

type VoteProposalInput struct {
	Vote bool `json:"vote" binding:"required"`
}
