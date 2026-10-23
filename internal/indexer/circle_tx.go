package indexer

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/payout"
)

// A contribution and the circle round state it moves must land together. If
// they are written in separate transactions, a crash between them leaves the
// contribution recorded but the circle's round/total counters stale, and a
// reader can never tell which half of the write happened.
//
// Every such write therefore runs inside one transaction that first takes a
// transaction-scoped advisory lock keyed on the circle. The lock serialises
// concurrent work for the same circle across replicas without blocking other
// circles, and because it is transaction-scoped it is released by COMMIT or
// ROLLBACK alike -- a crashed or killed worker cannot strand it the way a
// session-level lock can.
const (
	sqlLockCircle = `SELECT pg_advisory_xact_lock($1)`

	sqlCountRoundContributors = `SELECT COUNT(DISTINCT user_id) FROM contributions WHERE circle_id = $1 AND round_number = $2`

	sqlCountActiveMembers = `SELECT COUNT(*) FROM circle_members WHERE circle_id = $1 AND status = 'active'`

	// total_contributions is incremented relatively rather than set from a
	// value read outside the transaction, so two contributions landing at once
	// cannot lose one another's amount. current_round only ever moves forward:
	// GREATEST makes a replayed or out-of-order event a no-op instead of
	// dragging the counter backwards.
	sqlApplyContribution = `UPDATE circles
		SET total_contributions = total_contributions + $2,
		    current_round = CASE WHEN $3 THEN GREATEST(current_round, $4) ELSE current_round END,
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`

	sqlAdvanceRound = `UPDATE circles
		SET current_round = GREATEST(current_round, $2), updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`
)

// ErrNoDatabase is returned when a transactional circle write is attempted
// without a database handle.
var ErrNoDatabase = fmt.Errorf("indexer: no database handle")

// circleLockKey derives the advisory lock key for a circle from its UUID.
//
// The key is a pure function of the circle ID, so every replica locks the same
// row for the same circle without a lookup. The sign bit is cleared to keep the
// key in the positive namespace, and the derived UUID bytes are random, so
// collisions between distinct circles are not a practical concern -- and a
// collision would only over-serialise two circles, never allow a lost update.
func circleLockKey(circleID uuid.UUID) int64 {
	key := binary.BigEndian.Uint64(circleID[:8]) &^ (uint64(1) << 63)
	const maxInt64 = uint64(^uint64(0) >> 1)
	if key > maxInt64 {
		return int64(maxInt64)
	}
	return int64(key)
}

// RoundAdvance reports whether a contribution completed a round and moved the
// circle's current round forward.
type RoundAdvance struct {
	Advanced  bool
	NextRound int
}

// applyContribution records a contribution and reconciles the circle's round
// state in a single transaction, serialised by the per-circle advisory lock.
//
// The returned error is apperrors.ErrConflict (unchanged) when the contribution
// was already recorded, so callers can keep treating a replay as a no-op.
func (p *EventProcessor) applyContribution(ctx context.Context, contrib *contribution.Contribution) (RoundAdvance, error) {
	var advance RoundAdvance
	if p.db == nil {
		return advance, ErrNoDatabase
	}

	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return advance, fmt.Errorf("beginning contribution transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, sqlLockCircle, circleLockKey(contrib.CircleID)); err != nil {
		return advance, fmt.Errorf("acquiring circle advisory lock: %w", err)
	}

	if err := contribution.NewRepositoryFromTx(tx).Create(ctx, contrib); err != nil {
		// Returned verbatim so the caller can still recognise a replay.
		return advance, err
	}

	advance, err = p.advanceRoundInTx(ctx, tx, contrib)
	if err != nil {
		return advance, err
	}

	if err := tx.Commit(); err != nil {
		return advance, fmt.Errorf("committing contribution: %w", err)
	}
	committed = true
	return advance, nil
}

// advanceRoundInTx reconciles the circle row for a contribution that has
// already been inserted in tx. It runs under the advisory lock, so the counts
// it reads include every contribution that committed before this one.
func (p *EventProcessor) advanceRoundInTx(ctx context.Context, tx *sqlx.Tx, contrib *contribution.Contribution) (RoundAdvance, error) {
	var advance RoundAdvance

	var contributed, activeMembers int
	if err := tx.QueryRowxContext(ctx, sqlCountRoundContributors, contrib.CircleID, contrib.RoundNumber).Scan(&contributed); err != nil {
		return advance, fmt.Errorf("counting round contributors: %w", err)
	}
	if err := tx.QueryRowxContext(ctx, sqlCountActiveMembers, contrib.CircleID).Scan(&activeMembers); err != nil {
		return advance, fmt.Errorf("counting active members: %w", err)
	}

	// The advance is conditional on the round being fully funded. A circle with
	// no active members never advances: without a denominator the round cannot
	// be known to be complete, and advancing on that guess would skip a round.
	complete := activeMembers > 0 && contributed >= activeMembers
	next := contrib.RoundNumber + 1

	if _, err := tx.ExecContext(ctx, sqlApplyContribution, contrib.CircleID, contrib.Amount, complete, next); err != nil {
		return advance, fmt.Errorf("applying contribution to circle: %w", err)
	}

	advance.Advanced = complete
	advance.NextRound = next
	return advance, nil
}

// recordPayoutAndAdvance records a payout and the round it closes in one
// transaction, under the same per-circle advisory lock. Returns
// apperrors.ErrConflict when the payout was already recorded.
func (p *EventProcessor) recordPayoutAndAdvance(ctx context.Context, po *payout.Payout) error {
	if p.db == nil {
		return ErrNoDatabase
	}

	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning payout transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, sqlLockCircle, circleLockKey(po.CircleID)); err != nil {
		return fmt.Errorf("acquiring circle advisory lock: %w", err)
	}

	if err := payout.NewRepositoryFromTx(tx).Create(ctx, po); err != nil {
		return err
	}

	// A payout closes the round, so the advance is unconditional here -- but
	// still forward-only, so a late or replayed payout cannot rewind the circle.
	if _, err := tx.ExecContext(ctx, sqlAdvanceRound, po.CircleID, po.RoundNumber+1); err != nil {
		return fmt.Errorf("advancing circle round: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing payout: %w", err)
	}
	committed = true
	return nil
}
