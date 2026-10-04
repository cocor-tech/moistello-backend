package governance

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Degradation
//
// A weight lookup is an external dependency. None of these failures may make a
// proposal unvotable or strip a voter of their vote.
// ---------------------------------------------------------------------------

// Without a resolver the pre-#418 behaviour must be preserved: one vote each,
// decided by head count.
func TestVote_WithoutResolverFallsBackToHeadCount(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil)
	creator := uuid.New().String()

	p := newProposal(t, svc, creator)
	require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), creator, true))
	require.NoError(t, svc.ExecuteProposal(ctx, p.ID.String()))

	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, ProposalStatusExecuted, after.Status)
	assert.Equal(t, int64(1), after.ForWeight, "every vote counts at least once")
}

// A resolver that cannot list voters must not block proposal creation.
func TestCreateProposal_ResolverFailureStillAllowsProposals(t *testing.T) {
	ctx := context.Background()
	voter := uuid.New().String()
	resolver := &mutableResolver{err: errors.New("token contract unreachable")}
	svc := newWeightedService(resolver)

	p, err := svc.CreateProposal(ctx, CreateProposalInput{
		Title: "Title", Description: "Desc", ProposalType: "parameter", CreatorID: voter,
	})
	require.NoError(t, err, "a weight lookup failure must not prevent proposal creation")
	assert.Empty(t, p.WeightSnapshot)

	// And the proposal is still votable.
	require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), voter, true))
	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, int64(1), after.ForWeight, "a voter must never be disenfranchised by a lookup failure")
}

// A voter missing from the snapshot still gets a vote rather than weight zero.
func TestVote_VoterAbsentFromSnapshotStillCounts(t *testing.T) {
	ctx := context.Background()
	snapshotted := uuid.New().String()
	newcomer := uuid.New().String()

	resolver := &mutableResolver{
		weights: map[string]int64{snapshotted: 10, newcomer: 7},
		voters:  []string{snapshotted},
	}
	svc := newWeightedService(resolver)
	p := newProposal(t, svc, snapshotted)

	require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), newcomer, true))

	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, int64(7), after.ForWeight, "an unsnapshotted voter falls back to their current weight")
}

// A per-voter weight failure must not silently record that voter as weight 0,
// which would make their vote worthless.
func TestCreateProposal_PartialWeightFailureStillRecordsVoter(t *testing.T) {
	ok := uuid.New().String()
	broken := uuid.New().String()

	resolver := &partialFailResolver{
		weights: map[string]int64{ok: 40, broken: 0},
		voters:  []string{ok, broken},
		failFor: broken,
	}
	svc := newWeightedService(resolver)

	p := newProposal(t, svc, ok)

	assert.Equal(t, int64(40), p.WeightSnapshot[uuid.MustParse(ok)])
	weight, present := p.WeightSnapshot[uuid.MustParse(broken)]
	require.True(t, present, "a failed lookup must not drop the voter from the snapshot")
	assert.GreaterOrEqual(t, weight, int64(1), "a failed lookup must not record zero weight")
}

// partialFailResolver fails weight lookups only for one named user.
type partialFailResolver struct {
	weights map[string]int64
	voters  []string
	failFor string
}

func (r *partialFailResolver) VotingWeight(_ context.Context, userID string) (int64, error) {
	if userID == r.failFor {
		return 0, errors.New("contract timeout")
	}
	return r.weights[userID], nil
}

func (r *partialFailResolver) EligibleVoters(context.Context) ([]string, error) {
	return r.voters, nil
}

// ---------------------------------------------------------------------------
// Reputation tier factor
// ---------------------------------------------------------------------------

func TestApplyTierFactor_ScalesByReputationTier(t *testing.T) {
	tests := []struct {
		balance int64
		tier    string
		want    int64
	}{
		{100, "Bronze", 100},
		{100, "Silver", 125},
		{100, "Gold", 150},
		{100, "Platinum", 175},
		{100, "Diamond", 200},
		// An unknown or missing tier must not disenfranchise the user.
		{100, "", 100},
		{100, "Unobtanium", 100},
		{0, "Gold", 0},
		// A negative balance must never produce negative weight.
		{-50, "Gold", 0},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%d", tt.tier, tt.balance), func(t *testing.T) {
			assert.Equal(t, tt.want, ApplyTierFactor(tt.balance, tt.tier))
		})
	}
}

func TestSnapshotRule_IsDocumentedForProposalReads(t *testing.T) {
	// The rule must be exposed, not just implied by the code, so an API reader
	// can tell why a vote carried the power it did.
	assert.NotEmpty(t, SnapshotRule)
	assert.Contains(t, SnapshotRule, "snapshotted at proposal creation")
}

// A higher reputation tier must give the same balance more weight, so
// reputation cannot be bought purely with tokens.
func TestApplyTierFactor_HigherTierWinsAtEqualBalance(t *testing.T) {
	bronze := ApplyTierFactor(1000, "Bronze")
	diamond := ApplyTierFactor(1000, "Diamond")
	assert.Greater(t, diamond, bronze)
}
