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
}
