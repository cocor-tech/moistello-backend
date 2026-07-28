package governance

import (
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
	ID            uuid.UUID      `json:"id" db:"id"`
	Title         string         `json:"title" db:"title"`
	Description   string         `json:"description" db:"description"`
	ProposalType  string         `json:"proposalType" db:"proposal_type"`
	CreatorID     uuid.UUID      `json:"creatorId" db:"creator_id"`
	Status        ProposalStatus `json:"status" db:"status"`
	ForVotes      int            `json:"forVotes" db:"for_votes"`
	AgainstVotes  int            `json:"againstVotes" db:"against_votes"`
	ExecutedAt    *time.Time     `json:"executedAt,omitempty" db:"executed_at"`
	CreatedAt     time.Time      `json:"createdAt" db:"created_at"`
	UpdatedAt     time.Time      `json:"updatedAt" db:"updated_at"`
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
