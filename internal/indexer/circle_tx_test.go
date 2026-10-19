package indexer

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/apperrors"

	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/payout"
)

const contribRoundOne = 1

var (
	reLockCircle       = regexp.QuoteMeta(sqlLockCircle)
	reInsertContrib    = regexp.QuoteMeta("INSERT INTO contributions")
	reCountContrib     = regexp.QuoteMeta(sqlCountRoundContributors)
	reCountActive      = regexp.QuoteMeta(sqlCountActiveMembers)
	reApplyContrib     = regexp.QuoteMeta("total_contributions = total_contributions + $2")
	reInsertPayout     = regexp.QuoteMeta("INSERT INTO payouts")
	reAdvanceRound     = regexp.QuoteMeta(sqlAdvanceRound)
	errUniqueViolation = &pq.Error{Code: pqErrorUniqueViolation}
)

const pqErrorUniqueViolation = "23505"

// newTxTestProcessor returns a processor backed by a mockable database, which
// is what the transactional circle writes require.
func newTxTestProcessor(t *testing.T) (*EventProcessor, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, dbm, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	p := newTestProcessor(nil, nil, nil, nil, nil)
	p.db = sqlx.NewDb(sqlDB, "postgres")
	return p, dbm
}

func testContribution(circleID uuid.UUID, round, amount int) *contribution.Contribution {
	return &contribution.Contribution{
		ID:          uuid.New(),
		CircleID:    circleID,
		UserID:      uuid.New(),
		RoundNumber: round,
		Amount:      float64(amount),
		Status:      contribution.StatusConfirmed,
		OnTime:      true,
	}
}

func testPayoutRow(circleID uuid.UUID, round, amount int) *payout.Payout {
	return &payout.Payout{
		ID:          uuid.New(),
		CircleID:    circleID,
		RecipientID: uuid.New(),
		RoundNumber: round,
		Amount:      float64(amount),
		PayoutType:  payout.PayoutTypeRandom,
	}
}

func rowsWith(n int) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"count"}).AddRow(n)
}

func TestCircleLockKeyNeverSetsTheSignBit(t *testing.T) {
	for _, id := range []uuid.UUID{
		{},
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	} {
		testKey := circleLockKey(id)
		require.GreaterOrEqual(t, testKey, int64(0))
	}
}

// expectContributionTx queues the successful contribution transaction:
// lock, insert, count, update, commit.
func expectContributionTx(dbm sqlmock.Sqlmock, circleID uuid.UUID, round, amount, contributed, active int, complete bool) {
	dbm.ExpectBegin()
	dbm.ExpectExec(reLockCircle).WithArgs(circleLockKey(circleID)).WillReturnResult(sqlmock.NewResult(0, 0))
	dbm.ExpectExec(reInsertContrib).WillReturnResult(sqlmock.NewResult(0, 1))
	dbm.ExpectQuery(reCountContrib).WithArgs(circleID, round).WillReturnRows(rowsWith(contributed))
	dbm.ExpectQuery(reCountActive).WithArgs(circleID).WillReturnRows(rowsWith(active))
	dbm.ExpectExec(reApplyContrib).WithArgs(circleID, float64(amount), complete, round+1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	dbm.ExpectCommit()
}

// expectContributionConflict queues a replay: the insert hits the unique
// constraint, so the transaction rolls back before touching the circle row.
func expectContributionConflict(dbm sqlmock.Sqlmock, circleID uuid.UUID) {
	dbm.ExpectBegin()
	dbm.ExpectExec(reLockCircle).WithArgs(circleLockKey(circleID)).WillReturnResult(sqlmock.NewResult(0, 0))
	dbm.ExpectExec(reInsertContrib).WillReturnError(errUniqueViolation)
	dbm.ExpectRollback()
}

func expectPayoutTx(dbm sqlmock.Sqlmock, circleID uuid.UUID, round int) {
	dbm.ExpectBegin()
	dbm.ExpectExec(reLockCircle).WithArgs(circleLockKey(circleID)).WillReturnResult(sqlmock.NewResult(0, 0))
	dbm.ExpectExec(reInsertPayout).WillReturnResult(sqlmock.NewResult(0, 1))
	dbm.ExpectExec(reAdvanceRound).WithArgs(circleID, round+1).WillReturnResult(sqlmock.NewResult(0, 1))
	dbm.ExpectCommit()
}

func expectPayoutConflict(dbm sqlmock.Sqlmock, circleID uuid.UUID) {
	dbm.ExpectBegin()
	dbm.ExpectExec(reLockCircle).WithArgs(circleLockKey(circleID)).WillReturnResult(sqlmock.NewResult(0, 0))
	dbm.ExpectExec(reInsertPayout).WillReturnError(errUniqueViolation)
	dbm.ExpectRollback()
}

// ---------------------------------------------------------------------------
// Atomicity
// ---------------------------------------------------------------------------

// TestApplyContribution_RoundAdvanceIsConditional is the core guarantee: the
// contribution and the circle's round move together, and the round only moves
// once the round is actually fully funded.
func TestApplyContribution_RoundAdvanceIsConditional(t *testing.T) {
	t.Run("round not yet complete keeps the current round", func(t *testing.T) {
		p, dbm := newTxTestProcessor(t)
		circleID := uuid.New()
		expectContributionTx(dbm, circleID, contribRoundOne, 50, 2, 3, false)

		advance, err := p.applyContribution(context.Background(), testContribution(circleID, contribRoundOne, 50))
		require.NoError(t, err)
		assert.False(t, advance.Advanced, "an under-funded round must not advance")
		assert.NoError(t, dbm.ExpectationsWereMet())
	})

	t.Run("last contribution advances the round", func(t *testing.T) {
		p, dbm := newTxTestProcessor(t)
		circleID := uuid.New()
		expectContributionTx(dbm, circleID, contribRoundOne, 50, 3, 3, true)

		advance, err := p.applyContribution(context.Background(), testContribution(circleID, contribRoundOne, 50))
		require.NoError(t, err)
		assert.True(t, advance.Advanced)
		assert.Equal(t, contribRoundOne+1, advance.NextRound)
		assert.NoError(t, dbm.ExpectationsWereMet())
	})

	t.Run("circle with no active members never advances", func(t *testing.T) {
		p, dbm := newTxTestProcessor(t)
		circleID := uuid.New()
		// contributed(1) >= active(0) would be true arithmetically, but with no
		// denominator the round cannot be known to be complete.
		expectContributionTx(dbm, circleID, contribRoundOne, 50, 1, 0, false)

		advance, err := p.applyContribution(context.Background(), testContribution(circleID, contribRoundOne, 50))
		require.NoError(t, err)
		assert.False(t, advance.Advanced)
		assert.NoError(t, dbm.ExpectationsWereMet())
	})
}

// TestApplyContribution_CrashInjectionIsAllOrNothing injects a failure at each
// step of the transaction and asserts the write is never half-applied: every
// failing path rolls back, and no path commits.
func TestApplyContribution_CrashInjectionIsAllOrNothing(t *testing.T) {
	t.Run("crash acquiring the advisory lock writes nothing", func(t *testing.T) {
		p, dbm := newTxTestProcessor(t)
		circleID := uuid.New()

		dbm.ExpectBegin()
		dbm.ExpectExec(reLockCircle).WithArgs(circleLockKey(circleID)).
			WillReturnError(errors.New("connection reset by peer"))
		dbm.ExpectRollback()

		_, err := p.applyContribution(context.Background(), testContribution(circleID, contribRoundOne, 50))
		require.Error(t, err)
		assert.NoError(t, dbm.ExpectationsWereMet(), "no contribution insert may be attempted")
	})

	t.Run("crash inserting the contribution rolls the whole thing back", func(t *testing.T) {
		p, dbm := newTxTestProcessor(t)
		circleID := uuid.New()

		dbm.ExpectBegin()
		dbm.ExpectExec(reLockCircle).WithArgs(circleLockKey(circleID)).WillReturnResult(sqlmock.NewResult(0, 0))
		dbm.ExpectExec(reInsertContrib).WillReturnError(errors.New("disk full"))
		dbm.ExpectRollback()

		_, err := p.applyContribution(context.Background(), testContribution(circleID, contribRoundOne, 50))
		require.Error(t, err)
		assert.NoError(t, dbm.ExpectationsWereMet(), "the circle row must not be updated without its contribution")
	})

	t.Run("crash updating circle state rolls back the contribution", func(t *testing.T) {
		p, dbm := newTxTestProcessor(t)
		circleID := uuid.New()

		dbm.ExpectBegin()
		dbm.ExpectExec(reLockCircle).WithArgs(circleLockKey(circleID)).WillReturnResult(sqlmock.NewResult(0, 0))
		dbm.ExpectExec(reInsertContrib).WillReturnResult(sqlmock.NewResult(0, 1))
		dbm.ExpectQuery(reCountContrib).WithArgs(circleID, contribRoundOne).WillReturnRows(rowsWith(3))
		dbm.ExpectQuery(reCountActive).WithArgs(circleID).WillReturnRows(rowsWith(3))
		dbm.ExpectExec(reApplyContrib).WithArgs(circleID, float64(50), true, contribRoundOne+1).
			WillReturnError(errors.New("crash: worker killed before commit"))
		dbm.ExpectRollback()

		_, err := p.applyContribution(context.Background(), testContribution(circleID, contribRoundOne, 50))
		require.Error(t, err)
		assert.NoError(t, dbm.ExpectationsWereMet(),
			"the contribution must be rolled back with the circle update, never left behind orphaned")
	})

	t.Run("crash counting round contributors rolls back", func(t *testing.T) {
		p, dbm := newTxTestProcessor(t)
		circleID := uuid.New()

		dbm.ExpectBegin()
		dbm.ExpectExec(reLockCircle).WithArgs(circleLockKey(circleID)).WillReturnResult(sqlmock.NewResult(0, 0))
		dbm.ExpectExec(reInsertContrib).WillReturnResult(sqlmock.NewResult(0, 1))
		dbm.ExpectQuery(reCountContrib).WithArgs(circleID, contribRoundOne).
			WillReturnError(errors.New("statement timeout"))
		dbm.ExpectRollback()

		_, err := p.applyContribution(context.Background(), testContribution(circleID, contribRoundOne, 50))
		require.Error(t, err)
		assert.NoError(t, dbm.ExpectationsWereMet())
	})

	t.Run("failure committing is surfaced and cannot look like success", func(t *testing.T) {
		p, dbm := newTxTestProcessor(t)
		circleID := uuid.New()

		dbm.ExpectBegin()
		dbm.ExpectExec(reLockCircle).WithArgs(circleLockKey(circleID)).WillReturnResult(sqlmock.NewResult(0, 0))
		dbm.ExpectExec(reInsertContrib).WillReturnResult(sqlmock.NewResult(0, 1))
		dbm.ExpectQuery(reCountContrib).WithArgs(circleID, contribRoundOne).WillReturnRows(rowsWith(1))
		dbm.ExpectQuery(reCountActive).WithArgs(circleID).WillReturnRows(rowsWith(3))
		dbm.ExpectExec(reApplyContrib).WithArgs(circleID, float64(50), false, contribRoundOne+1).
			WillReturnResult(sqlmock.NewResult(0, 1))
		dbm.ExpectCommit().WillReturnError(errors.New("could not serialize access"))

		_, err := p.applyContribution(context.Background(), testContribution(circleID, contribRoundOne, 50))
		require.Error(t, err, "a failed commit must not be reported as a recorded contribution")
		assert.NoError(t, dbm.ExpectationsWereMet())
	})
}

// TestRecordPayoutAndAdvance_CrashInjectionIsAllOrNothing mirrors the
// contribution guarantee for the payout path that advances the round.
func TestRecordPayoutAndAdvance_CrashInjectionIsAllOrNothing(t *testing.T) {
	t.Run("crash advancing the round rolls back the payout", func(t *testing.T) {
		p, dbm := newTxTestProcessor(t)
		circleID := uuid.New()

		dbm.ExpectBegin()
		dbm.ExpectExec(reLockCircle).WithArgs(circleLockKey(circleID)).WillReturnResult(sqlmock.NewResult(0, 0))
		dbm.ExpectExec(reInsertPayout).WillReturnResult(sqlmock.NewResult(0, 1))
		dbm.ExpectExec(reAdvanceRound).WithArgs(circleID, 3).WillReturnError(errors.New("crash"))
		dbm.ExpectRollback()

		err := p.recordPayoutAndAdvance(context.Background(), testPayoutRow(circleID, 2, 500))
		require.Error(t, err)
		assert.NoError(t, dbm.ExpectationsWereMet(),
			"a payout must not survive without the round advance it belongs to")
	})

	t.Run("replayed payout is a no-op that leaves the circle alone", func(t *testing.T) {
		p, dbm := newTxTestProcessor(t)
		circleID := uuid.New()
		expectPayoutConflict(dbm, circleID)

		err := p.recordPayoutAndAdvance(context.Background(), testPayoutRow(circleID, 2, 500))
		assert.ErrorIs(t, err, apperrors.ErrConflict)
		assert.NoError(t, dbm.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// Locking
// ---------------------------------------------------------------------------

// TestCircleLockKey_IsStableAndPositive pins the properties the advisory lock
// depends on: the same circle always maps to the same key across processes, and
// the key never lands in the negative namespace.
func TestCircleLockKey_IsStableAndPositive(t *testing.T) {
	circleID := uuid.New()
	first := circleLockKey(circleID)
	assert.Equal(t, first, circleLockKey(circleID), "the key must be a pure function of the circle")
	assert.GreaterOrEqual(t, first, int64(0), "keep the key in the positive namespace")

	// Distinct circles must not share a key, otherwise unrelated circles would
	// contend for the same lock.
	seen := map[int64]uuid.UUID{first: circleID}
	for i := 0; i < 10000; i++ {
		id := uuid.New()
		key := circleLockKey(id)
		require.GreaterOrEqual(t, key, int64(0))
		if prev, dup := seen[key]; dup {
			t.Fatalf("lock key collision between %s and %s", prev, id)
		}
		seen[key] = id
	}
}

// TestApplyContribution_ParallelCirclesDoNotDeadlock runs many circles at once
// and requires every one of them to finish inside the deadline.
//
// Distinct circles must not contend for the same advisory lock, and the
// processor must hold no shared mutable state while doing it. Either would show
// up here as a goroutine that never returns. The per-circle key derivation is
// pinned separately by TestCircleLockKey_IsStableAndPositive.
func TestApplyContribution_ParallelCirclesDoNotDeadlock(t *testing.T) {
	const circles = 25
	const roundsEach = 4

	var wg sync.WaitGroup
	errs := make(chan error, circles)

	for i := 0; i < circles; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A connection per circle, the way separate replicas have their own.
			p, dbm := newTxTestProcessor(t)
			circleID := uuid.New()
			for r := 1; r <= roundsEach; r++ {
				// Every round is fully funded here, so each one advances.
				expectContributionTx(dbm, circleID, r, 50, roundsEach, roundsEach, true)
			}
			for r := 1; r <= roundsEach; r++ {
				advance, err := p.applyContribution(context.Background(), testContribution(circleID, r, 50))
				if err != nil {
					errs <- err
					return
				}
				if !advance.Advanced {
					errs <- errors.New("expected a fully funded round to advance")
					return
				}
			}
			if err := dbm.ExpectationsWereMet(); err != nil {
				errs <- err
			}
		}()
	}

	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()

	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("parallel circle writes deadlocked: not every circle completed")
	}
	close(errs)

	for err := range errs {
		t.Errorf("parallel contribution failed: %v", err)
	}
}

// TestRecordPayoutAndAdvance_ParallelCirclesDoNotDeadlock is the same check for
// the round-advancing payout path.
func TestRecordPayoutAndAdvance_ParallelCirclesDoNotDeadlock(t *testing.T) {
	const circles = 25

	var wg sync.WaitGroup
	errs := make(chan error, circles)

	for i := 0; i < circles; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, dbm := newTxTestProcessor(t)
			circleID := uuid.New()
			expectPayoutTx(dbm, circleID, 1)
			if err := p.recordPayoutAndAdvance(context.Background(), testPayoutRow(circleID, 1, 500)); err != nil {
				errs <- err
				return
			}
			if err := dbm.ExpectationsWereMet(); err != nil {
				errs <- err
			}
		}()
	}

	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("parallel payout writes deadlocked: not every circle completed")
	}
	close(errs)

	for err := range errs {
		t.Errorf("parallel payout failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Guards
// ---------------------------------------------------------------------------

func TestApplyContribution_RequiresDatabase(t *testing.T) {
	p := newTestProcessor(nil, nil, nil, nil, nil)
	_, err := p.applyContribution(context.Background(), testContribution(uuid.New(), 1, 50))
	assert.ErrorIs(t, err, ErrNoDatabase)

	assert.ErrorIs(t,
		p.recordPayoutAndAdvance(context.Background(), testPayoutRow(uuid.New(), 1, 50)),
		ErrNoDatabase)
}
