package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/config"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// fakeChain is a scripted Horizon that serves a set of ledgers and can be
// swapped to a different branch to simulate a reorg.
type fakeChain struct {
	ledgers map[int64]Ledger
}

func (c *fakeChain) serve(w http.ResponseWriter, r *http.Request) {
	// Horizon paging tokens encode the ledger sequence in the high 32 bits.
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	sequence := cursor >> 32

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	records := make([]Ledger, 0, limit)
	for seq := sequence + 1; seq <= sequence+int64(limit); seq++ {
		l, ok := c.ledgers[seq]
		if !ok {
			break
		}
		records = append(records, l)
	}

	_ = json.NewEncoder(w).Encode(LedgerResponse{Embedded: struct {
		Records []Ledger `json:"records"`
	}{Records: records}})
}

// branch returns a server serving a linear chain, with ledger hashes suffixed
// by the branch name so two branches for the same sequence differ.
func branch(t *testing.T, branchName string, from, to int64) (*httptest.Server, *fakeChain) {
	t.Helper()
	chain := &fakeChain{ledgers: make(map[int64]Ledger, to-from+1)}
	for seq := from; seq <= to; seq++ {
		chain.ledgers[seq] = Ledger{Sequence: seq, Hash: "hash-" + branchName + "-" + strconv.FormatInt(seq, 10)}
	}
	srv := httptest.NewServer(http.HandlerFunc(chain.serve))
	t.Cleanup(srv.Close)
	return srv, chain
}

// recordBranch notes the hashes of an already-indexed branch in a history.
func recordBranch(h *LedgerHistory, branchName string, from, to int64) {
	for seq := from; seq <= to; seq++ {
		h.Record(seq, "hash-"+branchName+"-"+strconv.FormatInt(seq, 10))
	}
}

// fakeRewinder records rollback calls instead of touching a database.
type fakeRewinder struct {
	deletedFrom []int64
	deletedRows int64
	rewindTo    []int64
	deleteErr   error
	rewindErr   error
}

func (f *fakeRewinder) DeleteEventsFrom(_ context.Context, forkLedger int64) (int64, error) {
	if f.deleteErr != nil {
		return 0, f.deleteErr
	}
	f.deletedFrom = append(f.deletedFrom, forkLedger)
	return f.deletedRows, nil
}

func (f *fakeRewinder) Rewind(_ context.Context, lastLedger int64) error {
	if f.rewindErr != nil {
		return f.rewindErr
	}
	f.rewindTo = append(f.rewindTo, lastLedger)
	return nil
}

// ---------------------------------------------------------------------------
// LedgerHistory
// ---------------------------------------------------------------------------

func TestLedgerHistory_BoundedWindowEvictsOldest(t *testing.T) {
	h := NewLedgerHistory(3)
	for seq := int64(1); seq <= 5; seq++ {
		h.Record(seq, "hash-"+strconv.FormatInt(seq, 10))
	}

	assert.Equal(t, 3, h.Size(), "history must never exceed the window")
	assert.Equal(t, int64(3), h.Oldest())
	assert.Equal(t, int64(5), h.Newest())

	_, ok := h.Hash(1)
	assert.False(t, ok, "ledger 1 must have been evicted")
	_, ok = h.Hash(5)
	assert.True(t, ok)
}

func TestLedgerHistory_RecordIgnoresEmptyHash(t *testing.T) {
	h := NewLedgerHistory(5)
	h.Record(1, "good")
	h.Record(2, "")

	hash, ok := h.Hash(1)
	assert.True(t, ok)
	assert.Equal(t, "good", hash)

	_, ok = h.Hash(2)
	assert.False(t, ok, "a ledger with no hash must not be recorded")
}

func TestLedgerHistory_RecordOverwritesSameSequence(t *testing.T) {
	h := NewLedgerHistory(5)
	h.Record(1, "first")
	h.Record(1, "second")

	assert.Equal(t, 1, h.Size(), "re-recording a sequence must not grow the history")
	hash, _ := h.Hash(1)
	assert.Equal(t, "second", hash)
}

func TestLedgerHistory_TruncateDropsForkedLedgers(t *testing.T) {
	h := NewLedgerHistory(10)
	for seq := int64(1); seq <= 6; seq++ {
		h.Record(seq, "hash-"+strconv.FormatInt(seq, 10))
	}

	h.Truncate(4)

	assert.Equal(t, 3, h.Size())
	assert.Equal(t, int64(1), h.Oldest())
	assert.Equal(t, int64(3), h.Newest())
	_, ok := h.Hash(4)
	assert.False(t, ok)
}

func TestNewLedgerHistory_NonPositiveWindowFallsBackToDefault(t *testing.T) {
	assert.Equal(t, defaultReorgWindow, NewLedgerHistory(0).window)
	assert.Equal(t, defaultReorgWindow, NewLedgerHistory(-5).window)
}

// ---------------------------------------------------------------------------
// Detection
// ---------------------------------------------------------------------------

func TestChainReconciler_NoReorgWhenHashesMatch(t *testing.T) {
	srv, _ := branch(t, "a", 100, 110)
	poller := NewPoller(srv.URL, nil)
	history := NewLedgerHistory(10)
	recordBranch(history, "a", 100, 110)

	plan, err := (&ChainReconciler{Window: 10}).DetectReorg(context.Background(), poller, history, 110)
	require.NoError(t, err)
	assert.Nil(t, plan, "an unchanged chain must not produce a rewind plan")
}

func TestChainReconciler_DetectsForkAndReturnsPlan(t *testing.T) {
	// The chain was originally indexed on branch "a"...
	history := NewLedgerHistory(10)
	recordBranch(history, "a", 100, 110)

	// ...but Horizon now serves branch "b" from ledger 105 onwards, and
	// extends the chain to 115.
	srv, chain := branch(t, "b", 100, 115)
	for seq := int64(100); seq < 105; seq++ {
		chain.ledgers[seq] = Ledger{Sequence: seq, Hash: "hash-a-" + strconv.FormatInt(seq, 10)}
	}
	poller := NewPoller(srv.URL, nil)

	plan, err := (&ChainReconciler{Window: 10}).DetectReorg(context.Background(), poller, history, 110)
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.Equal(t, int64(105), plan.ForkLedger, "the lowest mismatching sequence is the fork point")
	assert.Equal(t, int64(104), plan.LastLedger, "the cursor must rewind to just before the fork")
}

func TestChainReconciler_ReorgDeeperThanWindowIsReported(t *testing.T) {
	// A window of 10 ending at 110 covers 101..110, so a fork at 100 sits at
	// the boundary and cannot be repaired from within the window.
	history := NewLedgerHistory(10)
	recordBranch(history, "a", 100, 110)

	srv, _ := branch(t, "b", 100, 112)
	poller := NewPoller(srv.URL, nil)

	plan, err := (&ChainReconciler{Window: 10}).DetectReorg(context.Background(), poller, history, 110)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrReorgTooDeep), "got %v", err)
	assert.Nil(t, plan, "an unrepairable reorg must not yield a partial plan")
}

// The window size is the configurable bound on how deep a reorg is repaired.
func TestChainReconciler_WindowSizeIsConfigurable(t *testing.T) {
	// A fork at 108 is inside a 5 ledger window but predates a 2 ledger one.
	history := NewLedgerHistory(20)
	recordBranch(history, "a", 100, 110)

	srv, chain := branch(t, "b", 100, 115)
	// Restore branch "a" below the fork so the only mismatch starts at 108.
	for seq := int64(100); seq < 108; seq++ {
		chain.ledgers[seq] = Ledger{Sequence: seq, Hash: "hash-a-" + strconv.FormatInt(seq, 10)}
	}
	poller := NewPoller(srv.URL, nil)

	// Window of 5 covers 106..110, so the fork at 108 is still inside it.
	inside, err := (&ChainReconciler{Window: 5}).DetectReorg(context.Background(), poller, history, 110)
	require.NoError(t, err)
	require.NotNil(t, inside)
	assert.Equal(t, int64(108), inside.ForkLedger)

	// Window of 2 covers only 109..110, which the fork at 108 predates.
	outside, err := (&ChainReconciler{Window: 2}).DetectReorg(context.Background(), poller, history, 110)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrReorgTooDeep), "got %v", err)
	assert.Nil(t, outside)
}

func TestChainReconciler_DisabledWhenWindowIsZero(t *testing.T) {
	srv, _ := branch(t, "b", 100, 110)
	poller := NewPoller(srv.URL, nil)
	history := NewLedgerHistory(10)
	history.Record(110, "hash-a-110")

	plan, err := (&ChainReconciler{Window: 0}).DetectReorg(context.Background(), poller, history, 110)
	require.NoError(t, err)
	assert.Nil(t, plan)
}

func TestChainReconciler_IgnoresLedgersOutsideRecordedHistory(t *testing.T) {
	// A freshly started process has no recorded hashes, so no mismatch can be
	// asserted and nothing should be rolled back.
	srv, _ := branch(t, "b", 100, 110)
	poller := NewPoller(srv.URL, nil)

	plan, err := (&ChainReconciler{Window: 10}).DetectReorg(context.Background(), poller, NewLedgerHistory(10), 110)
	require.NoError(t, err)
	assert.Nil(t, plan)
}

func TestChainReconciler_NoOpWhenCursorAtGenesis(t *testing.T) {
	srv, _ := branch(t, "a", 100, 110)
	poller := NewPoller(srv.URL, nil)

	plan, err := (&ChainReconciler{Window: 10}).DetectReorg(context.Background(), poller, NewLedgerHistory(10), 0)
	require.NoError(t, err)
	assert.Nil(t, plan)
}

// ---------------------------------------------------------------------------
// End-to-end convergence (#346 acceptance criterion)
// ---------------------------------------------------------------------------

// TestReorg_ConvergesToCorrectState simulates a reorg against the engine and
// asserts the indexer rolls the abandoned branch back, resumes from the fork
// and settles on the live chain, so the replacement branch is indexed exactly
// once and a follow-up check finds nothing left to repair.
func TestReorg_ConvergesToCorrectState(t *testing.T) {
	const (
		startLedger = int64(100)
		headLedger  = int64(110)
		forkLedger  = int64(108)
		liveHead    = int64(112)
	)
	const window = 10

	// Ledgers 100..110 are already indexed on branch "a".
	history := NewLedgerHistory(window)
	recordBranch(history, "a", startLedger, headLedger)

	// The network abandons branch "a" from 108, replacing it with branch "b"
	// and extending the chain to 112.
	srv, chain := branch(t, "b", startLedger, liveHead)
	for seq := startLedger; seq < forkLedger; seq++ {
		chain.ledgers[seq] = Ledger{Sequence: seq, Hash: "hash-a-" + strconv.FormatInt(seq, 10)}
	}
	poller := NewPoller(srv.URL, nil)

	rewinder := &fakeRewinder{deletedRows: 3}
	metrics := newUnregisteredMetrics()
	engine := &Engine{
		cfg:     config.IndexerConfig{BatchSize: 50, ReorgWindow: window},
		poller:  poller,
		history: history,
		chain:   &ChainReconciler{Window: window},
		rewind:  rewinder,
		metrics: metrics,
		dedup:   NewDeduplicator(time.Hour),
	}

	cursor := &Cursor{Chain: "stellar", LastLedger: headLedger, LastProcessedAt: time.Now()}

	// The reorg is detected and rolled back.
	got, err := engine.checkReorg(context.Background(), cursor)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, forkLedger-1, got.LastLedger, "cursor must resume immediately before the fork")

	require.Equal(t, []int64{forkLedger}, rewinder.deletedFrom,
		"events from the abandoned branch must be deleted from the fork point")
	require.Equal(t, []int64{forkLedger - 1}, rewinder.rewindTo)
	assert.Equal(t, float64(1), testutil.ToFloat64(metrics.ReorgsDetected))

	// The abandoned branch's hashes are forgotten, so replaying from the fork
	// re-records the new branch instead of comparing against the old one.
	_, ok := history.Hash(forkLedger)
	assert.False(t, ok, "the forked ledger's stale hash must be dropped")
	_, ok = history.Hash(forkLedger - 1)
	assert.True(t, ok, "ledgers before the fork are still valid")

	// Replay from the rewind point: the poll loop re-reads the live chain from
	// forkLedger and re-records each ledger as it processes it.
	replayed, err := poller.FetchLedgersInRange(context.Background(), got.LastLedger+1, liveHead)
	require.NoError(t, err)
	require.Len(t, replayed, int(liveHead-forkLedger+1), "every ledger from the fork must be replayed")
	for _, l := range replayed {
		history.Record(l.Sequence, l.Hash)
	}

	// Converged: every ledger at or after the fork now records the hash the
	// live chain serves, and none retains an abandoned-branch hash.
	for seq := forkLedger; seq <= liveHead; seq++ {
		recorded, ok := history.Hash(seq)
		require.True(t, ok, "ledger %d should be recorded after replay", seq)
		assert.Equal(t, chain.ledgers[seq].Hash, recorded, "ledger %d must converge on the live chain", seq)
	}

	// A follow-up check is clean: no repeated rollbacks.
	plan, err := engine.chain.DetectReorg(context.Background(), poller, history, liveHead)
	require.NoError(t, err)
	assert.Nil(t, plan, "state must be converged - a second check finds no reorg")
}

// ---------------------------------------------------------------------------
// Engine rollback wiring
// ---------------------------------------------------------------------------

func TestCheckReorg_PropagatesTooDeepError(t *testing.T) {
	history := NewLedgerHistory(10)
	recordBranch(history, "a", 100, 110)
	srv, _ := branch(t, "b", 100, 112)
	poller := NewPoller(srv.URL, nil)

	engine := &Engine{
		poller:  poller,
		history: history,
		chain:   &ChainReconciler{Window: 10},
		rewind:  &fakeRewinder{},
		metrics: newUnregisteredMetrics(),
	}

	_, err := engine.checkReorg(context.Background(), &Cursor{Chain: "stellar", LastLedger: 110})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrReorgTooDeep), "got %v", err)
}

func TestCheckReorg_RollbackFailureIsReturned(t *testing.T) {
	history := NewLedgerHistory(10)
	recordBranch(history, "a", 100, 110)
	srv, chain := branch(t, "b", 100, 112)
	for seq := int64(100); seq < 108; seq++ {
		chain.ledgers[seq] = Ledger{Sequence: seq, Hash: "hash-a-" + strconv.FormatInt(seq, 10)}
	}
	poller := NewPoller(srv.URL, nil)

	engine := &Engine{
		poller:  poller,
		history: history,
		chain:   &ChainReconciler{Window: 10},
		rewind:  &fakeRewinder{deleteErr: errors.New("delete failed")},
		metrics: newUnregisteredMetrics(),
	}

	_, err := engine.checkReorg(context.Background(), &Cursor{Chain: "stellar", LastLedger: 110})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reorg rollback")
}

func TestCheckReorg_CursorRewindFailureIsReturned(t *testing.T) {
	history := NewLedgerHistory(10)
	recordBranch(history, "a", 100, 110)
	srv, chain := branch(t, "b", 100, 112)
	for seq := int64(100); seq < 108; seq++ {
		chain.ledgers[seq] = Ledger{Sequence: seq, Hash: "hash-a-" + strconv.FormatInt(seq, 10)}
	}
	poller := NewPoller(srv.URL, nil)

	engine := &Engine{
		poller:  poller,
		history: history,
		chain:   &ChainReconciler{Window: 10},
		rewind:  &fakeRewinder{rewindErr: errors.New("cursor write failed")},
		metrics: newUnregisteredMetrics(),
	}

	_, err := engine.checkReorg(context.Background(), &Cursor{Chain: "stellar", LastLedger: 110})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reorg rollback")
}

func TestCheckReorg_NoOpWhenUnchanged(t *testing.T) {
	srv, _ := branch(t, "a", 100, 110)
	engine := &Engine{
		poller:  NewPoller(srv.URL, nil),
		history: NewLedgerHistory(10),
		chain:   &ChainReconciler{Window: 10},
		rewind:  &fakeRewinder{},
		metrics: newUnregisteredMetrics(),
	}

	cursor := &Cursor{Chain: "stellar", LastLedger: 110, LastProcessedAt: time.Now()}
	got, err := engine.checkReorg(context.Background(), cursor)
	require.NoError(t, err)
	assert.Same(t, cursor, got, "an unchanged chain returns the same cursor")
}

func TestCheckReorg_NoOpWhenNotWired(t *testing.T) {
	// An engine built without reorg support (nil fields) must not panic.
	engine := &Engine{}
	cursor := &Cursor{Chain: "stellar", LastLedger: 110, LastProcessedAt: time.Now()}

	got, err := engine.checkReorg(context.Background(), cursor)
	require.NoError(t, err)
	assert.Same(t, cursor, got)
}

// ---------------------------------------------------------------------------
// Poller range helper
// ---------------------------------------------------------------------------

func TestPoller_FetchLedgersInRange(t *testing.T) {
	srv, _ := branch(t, "a", 100, 120)
	poller := NewPoller(srv.URL, nil)

	got, err := poller.FetchLedgersInRange(context.Background(), 105, 108)
	require.NoError(t, err)
	require.Len(t, got, 4)
	assert.Equal(t, int64(105), got[0].Sequence)
	assert.Equal(t, int64(108), got[3].Sequence)
}

func TestPoller_FetchLedgersInRange_InvertedRange(t *testing.T) {
	srv, _ := branch(t, "a", 100, 120)
	poller := NewPoller(srv.URL, nil)

	got, err := poller.FetchLedgersInRange(context.Background(), 110, 105)
	require.NoError(t, err)
	assert.Empty(t, got)
}
