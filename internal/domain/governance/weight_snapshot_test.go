package governance

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mutableResolver is a WeightResolver whose weights can be changed at any time
// by the test, standing in for token balances and reputation moving underfoot.
type mutableResolver struct {
	weights map[string]int64
	voters  []string
	err     error
	// lookups counts how many times a live weight was actually read, so a test
	// can prove a vote did not consult it.
	lookups int
}

func (r *mutableResolver) VotingWeight(_ context.Context, userID string) (int64, error) {
	r.lookups++
	if r.err != nil {
		return 0, r.err
	}
	return r.weights[userID], nil
}

func (r *mutableResolver) EligibleVoters(context.Context) ([]string, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.voters, nil
}

// newWeightedService builds a service with the given resolver. It takes the
// interface so tests can supply resolvers with specific failure modes.
func newWeightedService(r WeightResolver, opts ...Option) Service {
	return NewService(nil, append([]Option{WithWeightResolver(r)}, opts...)...)
}

func newProposal(t *testing.T, svc Service, creatorID string) *Proposal {
	t.Helper()
	p, err := svc.CreateProposal(context.Background(), CreateProposalInput{
		Title:        "Raise the circle cap",
		Description:  "Allow larger circles",
		ProposalType: "parameter",
		CreatorID:    creatorID,
	})
	require.NoError(t, err)
	return p
}

// ---------------------------------------------------------------------------
// Snapshot at creation (#418)
// ---------------------------------------------------------------------------

func TestCreateProposal_SnapshotsVoterWeightAtCreation(t *testing.T) {
	ctx := context.Background()
	rich := uuid.New().String()
	poor := uuid.New().String()

	resolver := &mutableResolver{
		weights: map[string]int64{rich: 500, poor: 10},
		voters:  []string{rich, poor},
	}
	svc := newWeightedService(resolver)

	p := newProposal(t, svc, rich)

	require.NotNil(t, p.SnapshotAt, "a proposal must record when its snapshot was taken")
	assert.Equal(t, int64(500), p.WeightSnapshot[uuid.MustParse(rich)])
	assert.Equal(t, int64(10), p.WeightSnapshot[uuid.MustParse(poor)])
	assert.Equal(t, int64(510), p.SnapshotTotalWeight)
}

// This is the issue's core requirement: weight moved after creation must not
// alter an in-flight vote's power.
func TestVote_WeightMovedAfterCreationDoesNotAlterVote(t *testing.T) {
	ctx := context.Background()
	voter := uuid.New().String()
	other := uuid.New().String()

	resolver := &mutableResolver{
		weights: map[string]int64{voter: 100, other: 50},
		voters:  []string{voter, other},
	}
	svc := newWeightedService(resolver)

	p := newProposal(t, svc, voter)
	require.Equal(t, int64(100), p.WeightSnapshot[uuid.MustParse(voter)])

	// The voter dumps their entire balance after the proposal is created. This
	// is the flash-vote-gaming move the snapshot exists to neutralise.
	resolver.weights[voter] = 0

	lookupsBefore := resolver.lookups
	err := svc.VoteProposal(ctx, p.ID.String(), voter, true)
	require.NoError(t, err)

// Acquiring tokens after creation must not help either — the mirror image of
// the same attack.
func TestVote_TokensAcquiredAfterCreationDoNotCount(t *testing.T) {
	ctx := context.Background()
	voter := uuid.New().String()

	resolver := &mutableResolver{
		weights: map[string]int64{voter: 5},
		voters:  []string{voter},
	}
	svc := newWeightedService(resolver)

	p := newProposal(t, svc, voter)

	// A whale arrives after the proposal is open.
	resolver.weights[voter] = 1_000_000

	require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), voter, true))

	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, int64(5), after.ForWeight,
		"tokens acquired after creation must not inflate an in-flight vote")
}

// A vote already cast keeps its power even after the tally has been read.
func TestVote_RepeatedReadsDoNotDriftAfterWeightMoves(t *testing.T) {
	ctx := context.Background()
	voter := uuid.New().String()

	resolver := &mutableResolver{
		weights: map[string]int64{voter: 42},
		voters:  []string{voter},
	}
	svc := newWeightedService(resolver)
	p := newProposal(t, svc, voter)

	require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), voter, true))
	resolver.weights[voter] = 9999

	for i := 0; i < 3; i++ {
		after, err := svc.GetProposal(ctx, p.ID.String())
		require.NoError(t, err)
		assert.Equal(t, int64(42), after.ForWeight, "the tally must not drift")
	}
}

// ---------------------------------------------------------------------------
// Weighted outcomes
// ---------------------------------------------------------------------------

func TestExecuteProposal_WeightedTallyDecidesOutcome(t *testing.T) {
	ctx := context.Background()
	whale := uuid.New().String()
	minnow := uuid.New().String()

	resolver := &mutableResolver{
		weights: map[string]int64{whale: 100, minnow: 1},
		voters:  []string{whale, minnow},
	}
	svc := newWeightedService(resolver)
	p := newProposal(t, svc, whale)

	// Head count would tie 1-1; weight must break it decisively.
	require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), whale, true))
	require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), minnow, false))

	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, 1, after.ForVotes)
	assert.Equal(t, 1, after.AgainstVotes, "head counts are tied")
	assert.Equal(t, int64(100), after.ForWeight)
	assert.Equal(t, int64(1), after.AgainstWeight)

	require.NoError(t, svc.ExecuteProposal(ctx, p.ID.String()))
	executed, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, ProposalStatusExecuted, executed.Status,
		"the weighted tally should pass despite the tied head count")
}

func TestExecuteProposal_TiedWeightDoesNotPass(t *testing.T) {
	ctx := context.Background()
	a, b := uuid.New().String(), uuid.New().String()

	resolver := &mutableResolver{
		weights: map[string]int64{a: 50, b: 50},
		voters:  []string{a, b},
	}
	svc := newWeightedService(resolver)
	p := newProposal(t, svc, a)

	require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), a, true))
	require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), b, false))
	require.NoError(t, svc.ExecuteProposal(ctx, p.ID.String()))

	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, ProposalStatusRejected, after.Status, "an exact weight tie must not pass")
}


	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)

	// The vote counts at the snapshotted 100, not the current 0.
	assert.Equal(t, int64(100), after.ForWeight, "vote power must come from the snapshot, not the live balance")
	assert.Equal(t, int64(0), after.AgainstWeight)
	assert.Equal(t, resolver.lookups, lookupsBefore,
		"voting must not perform a live weight lookup for a snapshotted voter")
}
