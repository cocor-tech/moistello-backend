package governance

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dummyExecutor struct {
	executed bool
	lastProp *Proposal
	err      error
}

func (e *dummyExecutor) ExecuteProposalAction(ctx context.Context, p *Proposal) error {
	if e.err != nil {
		return e.err
	}
	e.executed = true
	e.lastProp = p
	return nil
}

func TestProposalLifecycle(t *testing.T) {
	ctx := context.Background()
	executor := &dummyExecutor{}
	svc := NewService(nil, WithExecutor(executor))
	creatorID := uuid.New()

	created, err := svc.CreateProposal(ctx, CreateProposalInput{
		Title:        "Increase payout frequency",
		Description:  "Allow weekly payouts for active circles",
		ProposalType: "circle_action",
		CreatorID:    creatorID.String(),
	})
	require.NoError(t, err)
	require.Equal(t, ProposalStatusPending, created.Status)

	proposal, err := svc.GetProposal(ctx, created.ID.String())
	require.NoError(t, err)
	require.Equal(t, 0, proposal.ForVotes)

	// Vote for
	err = svc.VoteProposal(ctx, created.ID.String(), creatorID.String(), true)
	require.NoError(t, err)

	proposal, err = svc.GetProposal(ctx, created.ID.String())
	require.NoError(t, err)
	require.Equal(t, 1, proposal.ForVotes)
	require.Equal(t, ProposalStatusPending, proposal.Status)

	// Execute proposal (for_votes > against_votes -> passed & executed)
	err = svc.ExecuteProposal(ctx, created.ID.String())
	require.NoError(t, err)

	proposal, err = svc.GetProposal(ctx, created.ID.String())
	require.NoError(t, err)
	require.Equal(t, ProposalStatusExecuted, proposal.Status)
	require.NotNil(t, proposal.ExecutedAt)
	require.True(t, executor.executed)
	require.Equal(t, created.ID, executor.lastProp.ID)
}

func TestProposalRejection(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil)
	creatorID := uuid.New()
	voter2 := uuid.New()

	created, err := svc.CreateProposal(ctx, CreateProposalInput{
		Title:        "Bad proposal",
		Description:  "This should be rejected",
		ProposalType: "parameter",
		CreatorID:    creatorID.String(),
	})
	require.NoError(t, err)

	// One against vote
	err = svc.VoteProposal(ctx, created.ID.String(), voter2.String(), false)
	require.NoError(t, err)

	// Execute -> should mark as rejected
	err = svc.ExecuteProposal(ctx, created.ID.String())
	require.NoError(t, err)

	proposal, err := svc.GetProposal(ctx, created.ID.String())
	require.NoError(t, err)
	require.Equal(t, ProposalStatusRejected, proposal.Status)
	require.Nil(t, proposal.ExecutedAt)
}

func TestDuplicateVoting(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil)
	creatorID := uuid.New()

	created, err := svc.CreateProposal(ctx, CreateProposalInput{
		Title:        "Test proposal",
		Description:  "Description",
		ProposalType: "parameter",
		CreatorID:    creatorID.String(),
	})
	require.NoError(t, err)

	err = svc.VoteProposal(ctx, created.ID.String(), creatorID.String(), true)
	require.NoError(t, err)

	err = svc.VoteProposal(ctx, created.ID.String(), creatorID.String(), false)
	require.ErrorIs(t, err, ErrAlreadyVoted)
}

func TestProposalPagination(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil)
	creatorID := uuid.New()

	for i := 1; i <= 15; i++ {
		_, err := svc.CreateProposal(ctx, CreateProposalInput{
			Title:        fmt.Sprintf("Proposal %d", i),
			Description:  "Desc",
			ProposalType: "test",
			CreatorID:    creatorID.String(),
		})
		require.NoError(t, err)
	}

	proposals, total, err := svc.ListProposals(ctx, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, 15, total)
	assert.Len(t, proposals, 10)

	proposalsPage2, total, err := svc.ListProposals(ctx, 2, 10)
	require.NoError(t, err)
	assert.Equal(t, 15, total)
	assert.Len(t, proposalsPage2, 5)
}

func TestProposalNotFoundAndInactive(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil)

	// Get non-existent
	_, err := svc.GetProposal(ctx, uuid.New().String())
	assert.ErrorIs(t, err, ErrProposalNotFound)

	// Vote non-existent
	err = svc.VoteProposal(ctx, uuid.New().String(), uuid.New().String(), true)
	assert.ErrorIs(t, err, ErrProposalNotFound)

	// Create and execute
	creatorID := uuid.New()
	p, err := svc.CreateProposal(ctx, CreateProposalInput{
		Title:        "Title",
		Description:  "Desc",
		ProposalType: "test",
		CreatorID:    creatorID.String(),
	})
	require.NoError(t, err)

	err = svc.ExecuteProposal(ctx, p.ID.String())
	require.NoError(t, err)

	// Try voting on executed proposal
	err = svc.VoteProposal(ctx, p.ID.String(), creatorID.String(), true)
	assert.ErrorContains(t, err, "no longer active")

	// Try re-executing
	err = svc.ExecuteProposal(ctx, p.ID.String())
	assert.ErrorContains(t, err, "already been processed")
}
