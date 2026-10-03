package indexer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// Cursor tracks the last processed ledger for a given chain.
type Cursor struct {
	Chain           string    `db:"chain" json:"chain"`
	LastLedger      int64     `db:"last_ledger" json:"lastLedger"`
	LastProcessedAt time.Time `db:"last_processed_at" json:"lastProcessedAt"`
}

// Lag returns how long it has been since the cursor last advanced, relative
// to now. A growing lag indicates the poll loop is stuck or falling behind.
func (c *Cursor) Lag(now time.Time) time.Duration {
	return now.Sub(c.LastProcessedAt)
}

// CursorTracker persists and retrieves the indexer cursor in PostgreSQL.
type CursorTracker struct {
	db *sqlx.DB
}

// NewCursorTracker creates a new CursorTracker backed by the given database.
func NewCursorTracker(db *sqlx.DB) *CursorTracker {
	return &CursorTracker{db: db}
}

// GetCurrent reads the current cursor from the database.
func (c *CursorTracker) GetCurrent(ctx context.Context) (*Cursor, error) {
	var cursor Cursor
	err := c.db.GetContext(ctx, &cursor, "SELECT chain, last_ledger, last_processed_at FROM indexer_cursor WHERE chain = 'stellar'")
	if err != nil {
		return nil, fmt.Errorf("reading cursor: %w", err)
	}
	return &cursor, nil
}

// Rewind moves the cursor backwards to lastLedger. Update deliberately only
// advances the cursor, so a reorg rollback needs this separate path (#346).
func (c *CursorTracker) Rewind(ctx context.Context, lastLedger int64) error {
	_, err := c.db.ExecContext(ctx,
		"UPDATE indexer_cursor SET last_ledger = $1, last_processed_at = $2 WHERE chain = 'stellar'",
		lastLedger, time.Now())
	if err != nil {
		return fmt.Errorf("rewinding cursor: %w", err)
	}
	return nil
}
// ErrCursorMissing is returned when the cursor row does not exist, so a
// checkpoint would otherwise be silently dropped.
var ErrCursorMissing = errors.New("indexer cursor row is missing")

// Update writes the new cursor position after successful processing.
// It is parallel-safe and monotonic: it only advances the cursor if
// lastLedger > current last_ledger; an older or equal ledger is a no-op.
// It fails loudly if no cursor row exists, so progress is never lost silently.
func (c *CursorTracker) Update(ctx context.Context, lastLedger int64) error {
	if lastLedger < 0 {
		return fmt.Errorf("updating cursor: invalid ledger %d", lastLedger)
	}
	res, err := c.db.ExecContext(ctx,
		"UPDATE indexer_cursor SET last_ledger = $1, last_processed_at = $2 WHERE chain = 'stellar' AND last_ledger < $1",
		lastLedger, time.Now())
	if err != nil {
		return fmt.Errorf("updating cursor: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return nil
	}

	// Nothing updated: either the cursor is already at/after lastLedger
	// (fine) or the row is missing (not fine).
	var current int64
	err = c.db.GetContext(ctx, &current, "SELECT last_ledger FROM indexer_cursor WHERE chain = 'stellar'")
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCursorMissing
	}
	if err != nil {
		return fmt.Errorf("verifying cursor: %w", err)
	}
	return nil
}
