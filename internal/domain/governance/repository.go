package governance

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Repository defines persistence operations for governance proposals and votes.
type Repository interface {
	CreateProposal(ctx context.Context, p *Proposal) error
	GetProposal(ctx context.Context, id uuid.UUID) (*Proposal, error)
	ListProposals(ctx context.Context, page, limit int) ([]Proposal, int, error)
	// RecordVoteWeighted records a vote together with the weight it was cast
	// at. The weight is the proposal's creation-time snapshot value, never a
	// live balance (#418), so the audit trail records the power actually used.
	RecordVoteWeighted(ctx context.Context, proposalID, voterID uuid.UUID, vote bool, weight int64) error
	HasVoted(ctx context.Context, proposalID, voterID uuid.UUID) (bool, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status ProposalStatus, executedAt *time.Time) error

	// MarkPassed records that a proposal carried its vote and is now waiting
	// out its execution timelock, stamping executableAt (#414). It is
	// conditional on the proposal still being pending, so two concurrent
	// callers cannot both open a timelock on the same proposal.
	MarkPassed(ctx context.Context, id uuid.UUID, executableAt time.Time) error

	// MarkCancelled records a cancellation inside the timelock window (#414).
	// Conditional on still being passed, so a proposal that was already
	// executed or cancelled cannot be cancelled afterwards.
	MarkCancelled(ctx context.Context, id uuid.UUID, cancelledAt time.Time) error

	// RecordCancellationVote records one member's vote to cancel, reporting
	// false if they had already voted to cancel (#414). Votes are not
	// reversible, matching the one-vote-per-user rule on the original ballot.
	RecordCancellationVote(ctx context.Context, proposalID, voterID uuid.UUID) (bool, error)
	// CountCancellationVotes returns how many members have voted to cancel.
	CountCancellationVotes(ctx context.Context, proposalID uuid.UUID) (int, error)
}
