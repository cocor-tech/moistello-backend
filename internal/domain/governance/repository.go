package governance

import (
	"context"

	"github.com/google/uuid"
)

// Repository defines persistence operations for governance proposals and votes.
type Repository interface {
	CreateProposal(ctx context.Context, p *Proposal) error
	GetProposal(ctx context.Context, id uuid.UUID) (*Proposal, error)
	ListProposals(ctx context.Context, page, limit int) ([]Proposal, int, error)
	RecordVote(ctx context.Context, proposalID, voterID uuid.UUID, vote bool) error
	HasVoted(ctx context.Context, proposalID, voterID uuid.UUID) (bool, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status ProposalStatus, executedAt *time.Time) error
}
