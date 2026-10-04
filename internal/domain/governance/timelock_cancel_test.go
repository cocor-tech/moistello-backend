package governance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// passedProposalWithVotes returns a proposal sitting in its timelock, cast with
// the given number of for and against votes.
func passedProposalWithVotes(t *testing.T, svc Service, p *Proposal, forVotes, againstVotes int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < forVotes; i++ {
		require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), uuid.New().String(), true))
	}
	for i := 0; i < againstVotes; i++ {
		require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), uuid.New().String(), false))
	}
	require.NoError(t, svc.ExecuteProposal(ctx, p.ID.String()))
}

// ---------------------------------------------------------------------------
// 3. Cancellation by threshold vote inside the timelock
// ---------------------------------------------------------------------------

// A lone member must not be able to cancel a broadly-backed proposal.
func TestCancel_BelowThresholdDoesNotCancel(t *testing.T) {
	ctx := context.Background()
	svc, _ := timelockFixture(nil, 48*time.Hour, 50) // 50% of 6 votes = 3
	creator := uuid.New().String()
	p := createTimelockProposal(t, svc, creator)
	passedProposalWithVotes(t, svc, p, 5, 1) // 6 votes cast -> threshold 3

	cancelled, err := svc.CancelProposal(ctx, p.ID.String(), uuid.New().String())
	require.NoError(t, err)
	assert.False(t, cancelled, "one vote is below the threshold")

	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, ProposalStatusPassed, after.Status, "the proposal must still await execution")
	assert.Equal(t, 1, after.CancellationVotes, "the vote still counts toward the threshold")
}

// Reaching the threshold cancels the proposal, and it can never execute after.
func TestCancel_ReachingThresholdCancels(t *testing.T) {
	ctx := context.Background()
	executor := &dummyExecutor{}
	svc, clk := timelockFixture(executor, 48*time.Hour, 50) // 6 votes -> threshold 3
	creator := uuid.New().String()
	p := createTimelockProposal(t, svc, creator)
	passedProposalWithVotes(t, svc, p, 5, 1)

	// Two votes fall short...
	for i := 0; i < 2; i++ {
		cancelled, err := svc.CancelProposal(ctx, p.ID.String(), uuid.New().String())
		require.NoError(t, err)
		require.False(t, cancelled)
	}

	// ...the third reaches it.
	cancelled, err := svc.CancelProposal(ctx, p.ID.String(), uuid.New().String())
	require.NoError(t, err)
	assert.True(t, cancelled, "the third vote reaches the threshold")

	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, ProposalStatusCancelled, after.Status)
	require.NotNil(t, after.CancelledAt)

	// A cancelled proposal must never execute, even long after the timelock.
	// The execution attempt is refused rather than silently succeeding, so a
	// caller is never told a cancelled proposal executed.
	clk.advance(100 * time.Hour)
	require.Error(t, svc.ExecuteProposal(ctx, p.ID.String()),
		"executing a cancelled proposal must be refused, not reported as a success")
	final, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, ProposalStatusCancelled, final.Status, "cancellation is terminal")
	assert.False(t, executor.executed, "the action must never run for a cancelled proposal")
}

// The same member cannot stack cancellation votes.
func TestCancel_OneVotePerMember(t *testing.T) {
	ctx := context.Background()
	svc, _ := timelockFixture(nil, 48*time.Hour, 100) // every vote needed
	creator := uuid.New().String()
	p := createTimelockProposal(t, svc, creator)
	passedProposalWithVotes(t, svc, p, 2, 0)

	member := uuid.New().String()
	_, err := svc.CancelProposal(ctx, p.ID.String(), member)
	require.NoError(t, err)

	_, err = svc.CancelProposal(ctx, p.ID.String(), member)
	assert.ErrorIs(t, err, ErrAlreadyCancelledVote,
		"a member must not inflate the count by voting repeatedly")
}

// The cancellation window closes the moment the timelock elapses.
func TestCancel_RefusedOnceTimelockElapses(t *testing.T) {
	ctx := context.Background()
	svc, clk := timelockFixture(nil, 48*time.Hour, 100)
	creator := uuid.New().String()
	p := createTimelockProposal(t, svc, creator)
	passedProposalWithVotes(t, svc, p, 2, 0)

	clk.advance(48 * time.Hour)
	_, err := svc.CancelProposal(ctx, p.ID.String(), uuid.New().String())
	assert.ErrorIs(t, err, ErrNotCancellable,
		"cancelling after executableAt must be refused")
}

// A proposal that never passed has no timelock window, so it is not cancellable.
func TestCancel_RefusedWhenNotPassed(t *testing.T) {
	ctx := context.Background()
	svc, _ := timelockFixture(nil, 48*time.Hour, 33)
	creator := uuid.New().String()
	p := createTimelockProposal(t, svc, creator)
	require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), creator, true))
	// Still pending: the vote has not been evaluated yet.

	_, err := svc.CancelProposal(ctx, p.ID.String(), uuid.New().String())
	assert.ErrorIs(t, err, ErrNotCancellable)
}

func TestCancel_RejectsInvalidIDs(t *testing.T) {
	ctx := context.Background()
	svc, _ := timelockFixture(nil, time.Hour, 33)

	_, err := svc.CancelProposal(ctx, "not-a-uuid", uuid.New().String())
	assert.ErrorContains(t, err, "invalid UUID")

	_, err = svc.CancelProposal(ctx, uuid.New().String(), "not-a-uuid")
	assert.ErrorContains(t, err, "invalid UUID")
}

func TestCancel_UnknownProposalIsNotFound(t *testing.T) {
	ctx := context.Background()
	svc, _ := timelockFixture(nil, time.Hour, 33)

	_, err := svc.CancelProposal(ctx, uuid.New().String(), uuid.New().String())
	assert.ErrorIs(t, err, ErrProposalNotFound)
}

// ---------------------------------------------------------------------------
// Threshold arithmetic
// ---------------------------------------------------------------------------

func TestCancellationThreshold(t *testing.T) {
	tests := []struct {
		total, pct, want int
	}{
		{0, 33, 1},  // nothing cast: still cancellable by one person
		{1, 33, 1},  // one voter, 33% rounds up to 1
		{3, 33, 1},  // 0.99 -> 1
		{6, 50, 3},  // exact
		{6, 33, 2},  // 1.98 -> 2
		{10, 33, 4}, // 3.3 -> 4
		{5, 100, 5}, // unanimity
		{7, 1, 1},   // never below 1
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, CancellationThreshold(tt.total, tt.pct),
			"threshold(%d votes, %d%%)", tt.total, tt.pct)
	}
}

func TestTimelockConfig_NormalizeClampsThreshold(t *testing.T) {
	// An unset or out-of-range threshold must fall back rather than disabling
	// cancellation or making it impossible.
	for _, pct := range []int{0, -5, 101, 1000} {
		got := TimelockConfig{CancelThresholdPct: pct}.Normalize()
		assert.Equal(t, DefaultCancelThresholdPct, got.CancelThresholdPct, "pct %d", pct)
	}
	// A valid value is preserved.
	assert.Equal(t, 40, TimelockConfig{CancelThresholdPct: 40}.Normalize().CancelThresholdPct)
}
