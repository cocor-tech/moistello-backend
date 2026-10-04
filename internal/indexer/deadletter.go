package indexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ErrDeadLetterNotFound is returned when an id does not match a dead-letter
// entry, or the entry has already been resolved.
var ErrDeadLetterNotFound = errors.New("indexer dead letter entry not found")

// DeadLetterEntry is an event the indexer could not process (#349).
type DeadLetterEntry struct {
	ID         uuid.UUID    `db:"id" json:"id"`
	Chain      string       `db:"chain" json:"chain"`
	TxHash     string       `db:"tx_hash" json:"txHash"`
	Ledger     int64        `db:"ledger" json:"ledger"`
	Error      string       `db:"error" json:"error"`
	Attempts   int          `db:"attempts" json:"attempts"`
	Payload    []byte       `db:"payload" json:"-"`
	Status     string       `db:"status" json:"status"`
	CreatedAt  time.Time    `db:"created_at" json:"createdAt"`
	UpdatedAt  time.Time    `db:"updated_at" json:"updatedAt"`
	ResolvedAt sql.NullTime `db:"resolved_at" json:"resolvedAt,omitempty"`
}

// DeadLetterStore records indexer events that could not be processed, so a
// failure is recoverable rather than silently lost. It is an interface so the
// engine can be tested without a database.
type DeadLetterStore interface {
	// Record adds a failed event to the dead-letter queue, or bumps the
	// attempt counter if the same transaction already failed. It returns the
	// entry's id.
	Record(ctx context.Context, entry *DeadLetterEntry) (string, error)
	// List returns unresolved entries, newest first, capped at limit.
	List(ctx context.Context, limit int) ([]DeadLetterEntry, error)
	// Count returns the number of unresolved entries.
	Count(ctx context.Context) (int, error)
	// Resolve marks an entry as handled, removing it from the backlog.
	Resolve(ctx context.Context, id string) error
}

// PGDeadLetterStore is the PostgreSQL-backed DeadLetterStore.
type PGDeadLetterStore struct {
	db *sqlx.DB
}

// NewDeadLetterStore creates a PostgreSQL-backed dead-letter store.
func NewDeadLetterStore(db *sqlx.DB) DeadLetterStore {
	return &PGDeadLetterStore{db: db}
}

// Record upserts the failure. tx_hash is unique, so a transaction that fails
// repeatedly updates one row and increments attempts instead of inserting a
// duplicate each time — otherwise a poison event would generate unbounded rows
// on every poll cycle.
func (s *PGDeadLetterStore) Record(ctx context.Context, entry *DeadLetterEntry) (string, error) {
	if entry == nil || entry.TxHash == "" {
		return "", fmt.Errorf("recording dead letter: tx hash is required")
	}
	if entry.Chain == "" {
		entry.Chain = "stellar"
	}
	if entry.Attempts <= 0 {
		entry.Attempts = 1
	}

	var payload interface{}
	if len(entry.Payload) > 0 {
		payload = entry.Payload
	}

	var id string
	err := s.db.QueryRowxContext(ctx, `
		INSERT INTO indexer_dead_letter (chain, tx_hash, ledger, error, attempts, payload, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'dead_letter')
		ON CONFLICT (tx_hash) DO UPDATE
			SET attempts = indexer_dead_letter.attempts + 1,
			    error = EXCLUDED.error,
			    ledger = EXCLUDED.ledger,
			    status = 'dead_letter',
			    resolved_at = NULL,
			    updated_at = NOW()
		RETURNING id
	`, entry.Chain, entry.TxHash, entry.Ledger, entry.Error, entry.Attempts, payload).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("recording dead letter for tx %s: %w", entry.TxHash, err)
	}
	return id, nil
}

// List returns the unresolved backlog, newest first.
func (s *PGDeadLetterStore) List(ctx context.Context, limit int) ([]DeadLetterEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var entries []DeadLetterEntry
	err := s.db.SelectContext(ctx, &entries, `
		SELECT id, chain, tx_hash, ledger, error, attempts, payload, status, created_at, updated_at, resolved_at
		FROM indexer_dead_letter
		WHERE status = 'dead_letter'
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing indexer dead letter entries: %w", err)
	}
	return entries, nil
}

// Count returns the size of the unresolved backlog.
func (s *PGDeadLetterStore) Count(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowxContext(ctx,
		`SELECT COUNT(*) FROM indexer_dead_letter WHERE status = 'dead_letter'`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting indexer dead letter entries: %w", err)
	}
	return count, nil
}

// Resolve marks an entry handled. Entries are resolved rather than deleted so
// the record of a past failure survives for audit.
func (s *PGDeadLetterStore) Resolve(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE indexer_dead_letter
		SET status = 'resolved', resolved_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND status = 'dead_letter'
	`, id)
	if err != nil {
		return fmt.Errorf("resolving indexer dead letter %s: %w", id, err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrDeadLetterNotFound
	}
	return nil
}

// MarshalPayload encodes a Horizon transaction for storage alongside the
// failure, so a dead-lettered event can be replayed later without re-fetching
// the ledger from Horizon.
func MarshalPayload(txn *Transaction) ([]byte, error) {
	if txn == nil {
		return nil, nil
	}
	b, err := json.Marshal(txn)
	if err != nil {
		return nil, fmt.Errorf("marshaling dead letter payload: %w", err)
	}
	return b, nil
}
