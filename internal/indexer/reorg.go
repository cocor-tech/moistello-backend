package indexer

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
)

// ChainReconciler detects Stellar ledger reorganizations and repairs the
// indexed state when one occurs (#346).
//
// A reorg replaces a run of already-closed ledgers with a different branch.
// Because the indexer advances its cursor monotonically, it would otherwise
// keep the rows derived from the abandoned branch and add the rows from the
// new one, leaving duplicate and conflicting events behind.
//
// Detection compares the ledger hashes in a bounded window behind the cursor
// against the hashes observed when those ledgers were first indexed. The
// lowest mismatching sequence is the fork point: everything from there up has
// to be rolled back and replayed. Bounding the window keeps the check cheap
// and, more importantly, keeps the rollback bounded — a reorg deeper than the
// window is reported rather than silently half-repaired.
//
// The window is the safety limit: within it the indexer converges to the
// correct state, beyond it an operator has to intervene.
type ChainReconciler struct {
	// Window is the number of ledgers behind the cursor inspected for a fork.
	// Values <= 0 disable reorg handling.
	Window int
}

// ReorgPlan describes the repair required for a detected reorganization.
// ForkLedger is the lowest ledger that changed and must be discarded;
// LastLedger is the new cursor position, i.e. ForkLedger-1.
type ReorgPlan struct {
	ForkLedger int64
	LastLedger int64
	// Scanned is how many ledgers were inspected to find the fork.
	Scanned int
}

// ErrReorgTooDeep is returned when a reorganization reaches further back than
// the configured window, so the indexer cannot determine a safe fork point on
// its own.
var ErrReorgTooDeep = fmt.Errorf("reorg deeper than the configured window")

// DetectReorg re-reads the ledgers in [lastLedger-Window+1, lastLedger] and
// compares them with the recorded hashes from the LedgerHistory. It returns
// nil when the chain is consistent, a plan when a fork is found inside the
// window, and ErrReorgTooDeep when the fork reaches past its start.
func (r *ChainReconciler) DetectReorg(ctx context.Context, poller *Poller, history *LedgerHistory, lastLedger int64) (*ReorgPlan, error) {
	if r.Window <= 0 || lastLedger <= 0 || poller == nil || history == nil {
		return nil, nil
	}

	from := lastLedger - int64(r.Window) + 1
	if from < 1 {
		from = 1
	}

	ledgers, err := poller.FetchLedgersInRange(ctx, from, lastLedger)
	if err != nil {
		return nil, fmt.Errorf("fetching reorg window: %w", err)
	}
	if len(ledgers) == 0 {
		return nil, nil
	}

	// The oldest ledger we can still compare against is the boundary of the
	// window. A fork at or below it cannot be repaired from here.
	boundary := ledgers[0].Sequence

	var fork int64
	for _, l := range ledgers {
		recorded, ok := history.Hash(l.Sequence)
		if !ok {
			// Not in the bounded history: nothing to compare, keep scanning.
			continue
		}
		if recorded == l.Hash {
			continue
		}
		if fork == 0 || l.Sequence < fork {
			fork = l.Sequence
		}
	}

	if fork == 0 {
		return nil, nil
	}

	if fork <= boundary {
		return nil, fmt.Errorf("%w: fork at ledger %d is at or below the window boundary %d (window=%d)", ErrReorgTooDeep, fork, boundary, r.Window)
	}

	log.Warn().
		Int64("forkLedger", fork).
		Int64("cursor", lastLedger).
		Int("scanned", len(ledgers)).
		Msg("ledger reorg detected — rolling back to fork point")

	return &ReorgPlan{ForkLedger: fork, LastLedger: fork - 1, Scanned: len(ledgers)}, nil
}
