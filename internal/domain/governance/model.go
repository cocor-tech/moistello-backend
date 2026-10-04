package governance

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type ProposalStatus string

const (
	ProposalStatusPending ProposalStatus = "pending"
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

	// Voting weight is snapshotted at creation time (#418) and votes are
	// counted against this snapshot rather than against live balances, so
	// tokens moved mid-vote cannot change an outcome.
	//
	// WeightSnapshot maps voter id -> the weight that voter held when this
	// proposal was created. ForWeight/AgainstWeight accumulate the snapshotted
	// weight of each vote; they are the authoritative tallies, while
	// ForVotes/AgainstVotes remain simple head counts for display.
	WeightSnapshot map[uuid.UUID]int64 `json:"weightSnapshot,omitempty" db:"-"`
	ForWeight      int64               `json:"forWeight" db:"for_weight"`
	AgainstWeight  int64               `json:"againstWeight" db:"against_weight"`
	// SnapshotAt is when the weight snapshot was taken. It is exposed so a
	// reader can see exactly which moment governs this proposal's votes.
	SnapshotAt *time.Time `json:"snapshotAt,omitempty" db:"snapshot_at"`
	// SnapshotTotalWeight is the sum of every snapshotted voter's weight, i.e.
	// the total voting power this proposal was created against.
	SnapshotTotalWeight int64 `json:"snapshotTotalWeight" db:"snapshot_total_weight"`

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

// SnapshotRule documents how voting weight is determined for a proposal, and is
// returned on proposal reads so the rule is visible to API consumers rather than
// only to people reading the source (#418).
//
// The rule: each voter's power is the token balance they held at the instant
// the proposal was created, multiplied by their reputation tier factor. Moving
// tokens or changing reputation after creation does not alter the power of any
// vote on that proposal — that is what prevents borrow-weight-vote-return
// gaming. The cost is that a user who acquires tokens after a proposal is
// created cannot vote on it with them.
const SnapshotRule = "Voting weight is snapshotted at proposal creation: each voter's power is their governance-token balance at that instant multiplied by their reputation tier factor. Votes are counted against this snapshot, so tokens or reputation acquired or moved after creation do not change the power of an in-flight vote."

// WeightResolver computes a voter's current voting weight: governance-token
// balance scaled by a reputation tier factor. Implemented in
// cmd/api-server/main.go over the token and reputation services, so the
// governance domain does not import them.
type WeightResolver interface {
	// VotingWeight returns the weight userID currently holds.
	VotingWeight(ctx context.Context, userID string) (int64, error)
	// EligibleVoters returns the users whose weight should be captured in a
	// proposal's snapshot. The snapshot must cover everyone who might later
	// vote, otherwise a voter with no entry could not be validated.
	EligibleVoters(ctx context.Context) ([]string, error)
}

// WeightSnapshotter is the default WeightResolver: every user has weight 1.
// It keeps proposals votable when no token/reputation wiring is present, which
// is the behaviour before #418.
type WeightSnapshotter struct{}

// VotingWeight returns 1 for every user.
func (WeightSnapshotter) VotingWeight(context.Context, string) (int64, error) { return 1, nil }

// EligibleVoters returns no candidates, so a snapshot built from it is empty
// and votes fall back to the default weight. See resolveVoteWeight.
func (WeightSnapshotter) EligibleVoters(context.Context) ([]string, error) { return nil, nil }

// TierWeightFactors maps a reputation tier to the multiplier applied to a
// voter's token balance. A higher reputation means more governance weight for
// the same tokens, so reputation cannot be bought purely with tokens.
//
// The factor is in percent to keep it integral: a Silver voter counts for 125.
var TierWeightFactors = map[string]int64{
	"Bronze":   100,
	"Silver":   125,
	"Gold":     150,
	"Platinum": 175,
	"Diamond":  200,
}

// DefaultTierFactor applies to a user with no reputation snapshot yet, and to
// any tier not in TierWeightFactors.
const DefaultTierFactor int64 = 100

// ApplyTierFactor multiplies a raw token balance by the reputation tier factor.
// An unknown or missing tier yields DefaultTierFactor rather than zero, so a
// user is never disenfranchised merely for lacking a reputation record.
func ApplyTierFactor(balance int64, tier string) int64 {
	factor, ok := TierWeightFactors[tier]
	if !ok {
		factor = DefaultTierFactor
	}
	if balance < 0 {
		return 0
	}
	return balance * factor / 100
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
