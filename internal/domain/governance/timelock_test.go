package governance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clock is a movable service clock so tests can cross a timelock boundary
// deterministically instead of sleeping.
type clock struct{ t time.Time }

func (c *clock) now() time.Time           { return c.t }
func (c *clock) advance(d time.Duration)   { c.t = c.t.Add(d) }
func (c *clock) reset()                    { c.t = baseTime }

// baseTime is the fixed instant every timelock test starts from.
var baseTime = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

// timelockFixture builds a service with a timelock, a controllable clock and
// (optionally) an executor, so a test can assert whether the action ran.
func timelockFixture(exec ProposalExecutor, delay time.Duration, cancelPct int) (Service, *clock) {
	clk := &clock{t: baseTime}
	opts := []Option{
		WithTimelock(TimelockConfig{Delay: delay, CancelThresholdPct: cancelPct}),
		withClock(clk.now),
	}
	if exec != nil {
		opts = append(opts, WithExecutor(exec))
	}
	return NewService(nil, opts...), clk
}

func createTimelockProposal(t *testing.T, svc Service, creator string) *Proposal {
	t.Helper()
	p, err := svc.CreateProposal(context.Background(), CreateProposalInput{
		Title:        "Drain the treasury",
		Description:  "Send the treasury balance to an address",
		ProposalType: "treasury",
		CreatorID:    creator,
	})
	require.NoError(t, err)
	return p
}

// passProposal drives a proposal to 'passed' by voting for it and calling
// execute once, which is what opens the timelock.
func passProposal(t *testing.T, svc Service, p *Proposal, voters ...string) {
	t.Helper()
	ctx := context.Background()
	for _, v := range voters {
		require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), v, true))
	}
	require.NoError(t, svc.ExecuteProposal(ctx, p.ID.String()))
}

// ---------------------------------------------------------------------------
// 1. The timelock blocks execution
// ---------------------------------------------------------------------------

func TestExecute_FirstCallStartsTimelockInsteadOfExecuting(t *testing.T) {
	ctx := context.Background()
	executor := &dummyExecutor{}
	svc, _ := timelockFixture(executor, 48*time.Hour, 33)
	creator := uuid.New().String()

	p := createTimelockProposal(t, svc, creator)
	passProposal(t, svc, p, creator)

	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)

	assert.Equal(t, ProposalStatusPassed, after.Status,
		"a carried proposal must wait out the timelock rather than executing")
	require.NotNil(t, after.ExecutableAt, "executableAt must be stamped so the UI can count down")
	assert.Equal(t, baseTime.Add(48*time.Hour), after.ExecutableAt.UTC())
	assert.False(t, executor.executed, "no action may run while the timelock is active")
}

func TestExecute_BlockedInsideTimelock(t *testing.T) {
	ctx := context.Background()
	executor := &dummyExecutor{}
	svc, clk := timelockFixture(executor, 48*time.Hour, 33)
	creator := uuid.New().String()
	p := createTimelockProposal(t, svc, creator)
	passProposal(t, svc, p, creator)

	// Every moment inside the window must refuse to execute.
	for _, offset := range []time.Duration{time.Second, time.Hour, 47 * time.Hour} {
		clk.reset()
		clk.advance(offset)
		err := svc.ExecuteProposal(ctx, p.ID.String())
		assert.ErrorIs(t, err, ErrTimelockActive, "executing at +%s must be refused", offset)
	}
	assert.False(t, executor.executed)
}

func TestExecute_AllowedOnceTimelockElapses(t *testing.T) {
	ctx := context.Background()
	executor := &dummyExecutor{}
	svc, clk := timelockFixture(executor, 48*time.Hour, 33)
	creator := uuid.New().String()
	p := createTimelockProposal(t, svc, creator)
	passProposal(t, svc, p, creator)

	// One second past the boundary is enough.
	clk.advance(48*time.Hour + time.Second)
	require.NoError(t, svc.ExecuteProposal(ctx, p.ID.String()))

	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, ProposalStatusExecuted, after.Status)
	assert.True(t, executor.executed, "the action must run once the timelock has passed")
}


// A zero timelock must preserve the pre-#414 behaviour: evaluate and execute in
// the same call, so a deployment that sets no delay is unaffected.
func TestExecute_ZeroTimelockExecutesImmediately(t *testing.T) {
	ctx := context.Background()
	executor := &dummyExecutor{}
	svc, _ := timelockFixture(executor, 0, 33)
	creator := uuid.New().String()
	p := createTimelockProposal(t, svc, creator)

	require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), creator, true))
	require.NoError(t, svc.ExecuteProposal(ctx, p.ID.String()))

	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, ProposalStatusExecuted, after.Status, "no timelock configured means immediate execution")
	assert.Nil(t, after.ExecutableAt)
	assert.True(t, executor.executed)
}

// A proposal that did not carry is rejected immediately — there is nothing to
// review, so no timelock is opened.
func TestExecute_RejectedProposalSkipsTimelock(t *testing.T) {
	ctx := context.Background()
	svc, _ := timelockFixture(nil, 48*time.Hour, 33)
	creator, voter := uuid.New().String(), uuid.New().String()
	p := createTimelockProposal(t, svc, creator)

	require.NoError(t, svc.VoteProposal(ctx, p.ID.String(), voter, false))
	require.NoError(t, svc.ExecuteProposal(ctx, p.ID.String()))

	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	assert.Equal(t, ProposalStatusRejected, after.Status)
	assert.Nil(t, after.ExecutableAt, "a rejected proposal has no execution window")
}

// Voting closes once a proposal passes, so nobody can add support after the
// decision and before execution.
func TestVote_ClosesWhenProposalPasses(t *testing.T) {
	ctx := context.Background()
	svc, _ := timelockFixture(nil, 48*time.Hour, 33)
	creator, latecomer := uuid.New().String(), uuid.New().String()
	p := createTimelockProposal(t, svc, creator)
	passProposal(t, svc, p, creator)

	err := svc.VoteProposal(ctx, p.ID.String(), latecomer, true)
	assert.ErrorContains(t, err, "no longer active",
		"the ballot must close once the proposal has passed")
}

// ---------------------------------------------------------------------------
// 2. executable_at is surfaced on reads
// ---------------------------------------------------------------------------

func TestProposal_ExposesExecutableAtAndRule(t *testing.T) {
	ctx := context.Background()
	svc, clk := timelockFixture(nil, 24*time.Hour, 33)
	creator := uuid.New().String()
	p := createTimelockProposal(t, svc, creator)
	passProposal(t, svc, p, creator)

	after, err := svc.GetProposal(ctx, p.ID.String())
	require.NoError(t, err)
	require.NotNil(t, after.ExecutableAt)

	// The countdown a client would render.
	assert.Equal(t, 24*time.Hour, after.ExecutableAt.Sub(clk.now()))

	// The rule is documented for reads, not just in the source.
	assert.NotEmpty(t, TimelockRule)
	assert.Contains(t, TimelockRule, "executableAt")
}

func TestListProposals_SurfacesTimelockFields(t *testing.T) {
	ctx := context.Background()
	svc, _ := timelockFixture(nil, 48*time.Hour, 33)
	creator := uuid.New().String()
	p := createTimelockProposal(t, svc, creator)
	passProposal(t, svc, p, creator)

	proposals, total, err := svc.ListProposals(ctx, 1, 10)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, proposals, 1)
	require.NotNil(t, proposals[0].ExecutableAt, "a listing must expose executableAt for the countdown")
}
