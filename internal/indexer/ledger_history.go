package indexer

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/jmoiron/sqlx"
)

// LedgerHistory is a bounded, in-memory record of the ledger hashes the
// indexer has already processed, used to detect reorgs (#346).
//
// The window is the point: it bounds both the work done per poll and the
// depth a reorganization can be repaired to. Ledgers that fall out of the
// window are forgotten, because a fork below the window is reported as
// unrepairable rather than silently mis-repaired.
//
// It is safe for concurrent use.
type LedgerHistory struct {
	mu     sync.RWMutex
	hashes map[int64]string
	order  []int64 // ascending ledger sequences, oldest first
	window int
}

// NewLedgerHistory creates a LedgerHistory retaining the last window ledgers.
// A window <= 0 falls back to defaultReorgWindow.
func NewLedgerHistory(window int) *LedgerHistory {
	if window <= 0 {
		window = defaultReorgWindow
	}
	return &LedgerHistory{
		hashes: make(map[int64]string, window),
		order:  make([]int64, 0, window),
		window: window,
	}
}

// Record notes the hash of a processed ledger, evicting the oldest entries
// once the window is full. Ledgers with an empty hash are ignored so a Horizon
// response that omits the field never blanks out a good record.
func (h *LedgerHistory) Record(sequence int64, hash string) {
	if hash == "" {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.hashes[sequence]; !exists {
		h.order = append(h.order, sequence)
		sort.Slice(h.order, func(i, j int) bool { return h.order[i] < h.order[j] })
	}
	h.hashes[sequence] = hash

	for len(h.order) > h.window {
		oldest := h.order[0]
		h.order = h.order[1:]
		delete(h.hashes, oldest)
	}
}

// Hash returns the recorded hash for a ledger sequence and whether it was
// still inside the window.
func (h *LedgerHistory) Hash(sequence int64) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	hash, ok := h.hashes[sequence]
	return hash, ok
}

// Truncate drops every record at or after sequence, so replayed ledgers are
// recorded afresh rather than compared against the abandoned branch.
func (h *LedgerHistory) Truncate(sequence int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	kept := h.order[:0]
	for _, s := range h.order {
		if s < sequence {
			kept = append(kept, s)
			continue
		}
		delete(h.hashes, s)
	}
	h.order = kept
}

// Size returns the number of retained ledger hashes.
func (h *LedgerHistory) Size() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.hashes)
}

// Oldest returns the earliest retained ledger sequence, or 0 when empty.
func (h *LedgerHistory) Oldest() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.order) == 0 {
		return 0
	}
	return h.order[0]
}

// Newest returns the latest retained ledger sequence, or 0 when empty.
func (h *LedgerHistory) Newest() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.order) == 0 {
		return 0
	}
	return h.order[len(h.order)-1]
}

// Rewinder rolls indexed state back to a ledger position. It is an interface
// so the engine can be tested without a live database.
type Rewinder interface {
	// DeleteEventsFrom removes every contract_events row for a ledger at or
	// after forkLedger, returning the number of rows removed.
	DeleteEventsFrom(ctx context.Context, forkLedger int64) (int64, error)
	// Rewind moves the cursor back to lastLedger. Unlike CursorTracker.Update
	// it is not guarded by a monotonic "only advance" condition, because a
	// rewind must be able to move the cursor backwards.
	Rewind(ctx context.Context, lastLedger int64) error
}

// PostgresRewinder implements Rewinder against PostgreSQL.
type PostgresRewinder struct {
	db     *sqlx.DB
	cursor *CursorTracker
}

// NewPostgresRewinder creates a Rewinder backed by the given database.
func NewPostgresRewinder(db *sqlx.DB, cursor *CursorTracker) *PostgresRewinder {
	return &PostgresRewinder{db: db, cursor: cursor}
}

// DeleteEventsFrom deletes the contract_events rows derived from the abandoned
// branch. The primary key is a synthetic uuid and tx_hash is not unique across
// ledgers, so the ledger number is what scopes the delete.
func (r *PostgresRewinder) DeleteEventsFrom(ctx context.Context, forkLedger int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, "DELETE FROM contract_events WHERE ledger >= $1", forkLedger)
	if err != nil {
		return 0, fmt.Errorf("deleting contract_events from ledger %d: %w", forkLedger, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		// Not every driver reports rows affected; the delete still happened.
		return 0, nil
	}
	return affected, nil
}

// Rewind moves the cursor back to lastLedger.
func (r *PostgresRewinder) Rewind(ctx context.Context, lastLedger int64) error {
	if r.cursor == nil {
		return fmt.Errorf("rewinding cursor: no cursor tracker configured")
	}
	if err := r.cursor.Rewind(ctx, lastLedger); err != nil {
		return fmt.Errorf("rewinding cursor to %d: %w", lastLedger, err)
	}
	return nil
}
