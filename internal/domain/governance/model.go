package governance

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type ProposalStatus string

const (
	ProposalStatusPending  ProposalStatus = "pending"
	ProposalStatusPassed   ProposalStatus = "passed"
	ProposalStatusRejected ProposalStatus = "rejected"
	ProposalStatusExecuted ProposalStatus = "executed"
)

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
	ForWeight      int64                `json:"forWeight" db:"for_weight"`
	AgainstWeight  int64                `json:"againstWeight" db:"against_weight"`
	// SnapshotAt is when the weight snapshot was taken. It is exposed so a
	// reader can see exactly which moment governs this proposal's votes.
	SnapshotAt *time.Time `json:"snapshotAt,omitempty" db:"snapshot_at"`
	// SnapshotTotalWeight is the sum of every snapshotted voter's weight, i.e.
	// the total voting power this proposal was created against.
	SnapshotTotalWeight int64 `json:"snapshotTotalWeight" db:"snapshot_total_weight"`
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

type CreateProposalInput struct {
	Title        string `json:"title" binding:"required"`
	Description  string `json:"description" binding:"required"`
	ProposalType string `json:"proposalType" binding:"required"`
	CreatorID    string `json:"creatorId" binding:"required"`
}

type VoteProposalInput struct {
	Vote bool `json:"vote" binding:"required"`
}
