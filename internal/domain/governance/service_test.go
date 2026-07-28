package governance

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestProposalLifecycle(t *testing.T) {
	svc := NewService()
	creatorID := uuid.New()

	created, err := svc.CreateProposal(context.Background(), CreateProposalInput{
		Title:       "Increase payout frequency",
		Description: "Allow weekly payouts for active circles",
		ProposalType: "parameter",
		CreatorID:   creatorID.String(),
	})
	require.NoError(t, err)
	require.Equal(t, ProposalStatusPending, created.Status)

	proposal, err := svc.GetProposal(context.Background(), created.ID.String())
	require.NoError(t, err)
	require.Equal(t, 0, proposal.ForVotes)

	err = svc.VoteProposal(context.Background(), created.ID.String(), creatorID.String(), true)
	require.NoError(t, err)

	proposal, err = svc.GetProposal(context.Background(), created.ID.String())
	require.NoError(t, err)
	require.Equal(t, 1, proposal.ForVotes)
	require.Equal(t, ProposalStatusPending, proposal.Status)
}
