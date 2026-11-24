package payout

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/apperrors"
)

var payoutColumns = []string{
	"id", "circle_id", "recipient_id", "round_number", "amount", "fee_amount",
	"txn_hash", "payout_type", "verified_onchain", "verification_status", "created_at",
}

func newPayoutRepoMock(t *testing.T) (Repository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewRepository(sqlx.NewDb(db, "sqlmock")), mock
}

func payoutRow(id uuid.UUID, txnHash driver.Value) []driver.Value {
	return []driver.Value{id, uuid.New(), uuid.New(), 1, 100.0, 1.0,
		txnHash, "fixed", true, "verified", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
}

func samplePayout() *Payout {
	return &Payout{
		ID:                 uuid.New(),
		CircleID:           uuid.New(),
		RecipientID:        uuid.New(),
		RoundNumber:        1,
		Amount:             100,
		PayoutType:         PayoutTypeFixed,
		VerificationStatus: VerificationStatusUnverified,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}
}

func TestPayoutPG_FindByID(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	t.Run("success scans nullable txn hash", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("FROM payouts WHERE id").WithArgs(id).
			WillReturnRows(sqlmock.NewRows(payoutColumns).AddRow(payoutRow(id, "txn-1")...))

		p, err := repo.FindByID(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, id, p.ID)
		assert.True(t, p.TxnHash.Valid)
		assert.Equal(t, "txn-1", p.TxnHash.String)
	})

	t.Run("null txn hash stays invalid", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("FROM payouts WHERE id").WillReturnRows(sqlmock.NewRows(payoutColumns).AddRow(payoutRow(id, nil)...))

		p, err := repo.FindByID(ctx, id)
		require.NoError(t, err)
		assert.False(t, p.TxnHash.Valid)
	})

	t.Run("missing row is not found", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("FROM payouts WHERE id").WillReturnRows(sqlmock.NewRows(payoutColumns))

		_, err := repo.FindByID(ctx, id)
		assert.ErrorIs(t, err, apperrors.ErrNotFound)
	})

	t.Run("scan failure is wrapped", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("FROM payouts WHERE id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id))

		_, err := repo.FindByID(ctx, id)
		assert.ErrorContains(t, err, "scanning payout row")
	})
}

func TestPayoutPG_FindByTxnHash(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		id := uuid.New()
		mock.ExpectQuery("FROM payouts WHERE txn_hash").WithArgs("txn-1").
			WillReturnRows(sqlmock.NewRows(payoutColumns).AddRow(payoutRow(id, "txn-1")...))

		p, err := repo.FindByTxnHash(ctx, "txn-1")
		require.NoError(t, err)
		assert.Equal(t, id, p.ID)
	})

	t.Run("missing row is not found", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("FROM payouts WHERE txn_hash").WillReturnRows(sqlmock.NewRows(payoutColumns))

		_, err := repo.FindByTxnHash(ctx, "txn-x")
		assert.ErrorIs(t, err, apperrors.ErrNotFound)
	})
}

func TestPayoutPG_Create(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectExec("INSERT INTO payouts").WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.Create(ctx, samplePayout()))
	})

	t.Run("unique violation maps to conflict", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectExec("INSERT INTO payouts").WillReturnError(&pq.Error{Code: "23505"})
		assert.ErrorIs(t, repo.Create(ctx, samplePayout()), apperrors.ErrConflict)
	})

	t.Run("other error is wrapped", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectExec("INSERT INTO payouts").WillReturnError(errors.New("db down"))
		err := repo.Create(ctx, samplePayout())
		assert.ErrorContains(t, err, "creating payout")
		assert.NotErrorIs(t, err, apperrors.ErrConflict)
	})
}

func TestPayoutPG_UpdateVerificationStatus(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectExec("UPDATE payouts SET verified_onchain").WithArgs(true, VerificationStatusVerified, id).
			WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.UpdateVerificationStatus(ctx, id, true, VerificationStatusVerified))
	})

	t.Run("no matching payout is not found", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectExec("UPDATE payouts SET verified_onchain").WillReturnResult(sqlmock.NewResult(0, 0))
		assert.ErrorIs(t, repo.UpdateVerificationStatus(ctx, id, false, VerificationStatusFailed), apperrors.ErrNotFound)
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectExec("UPDATE payouts SET verified_onchain").WillReturnError(errors.New("db down"))
		err := repo.UpdateVerificationStatus(ctx, id, false, VerificationStatusFailed)
		assert.ErrorContains(t, err, "updating payout verification status")
	})
}

func TestPayoutPG_ListByUser(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("paginates with defaults and returns total", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WithArgs(userID).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
		mock.ExpectQuery("ORDER BY created_at DESC LIMIT").WithArgs(userID, 20, 0).
			WillReturnRows(sqlmock.NewRows(payoutColumns).AddRow(payoutRow(uuid.New(), nil)...))

		payouts, total, err := repo.ListByUser(ctx, userID, 0, 0)
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		assert.Len(t, payouts, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("computes offset from page and limit", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery("ORDER BY created_at DESC LIMIT").WithArgs(userID, 5, 10).
			WillReturnRows(sqlmock.NewRows(payoutColumns))

		_, _, err := repo.ListByUser(ctx, userID, 3, 5)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count error", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("db down"))
		_, _, err := repo.ListByUser(ctx, userID, 1, 10)
		assert.ErrorContains(t, err, "counting user payouts")
	})

	t.Run("list error", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("ORDER BY created_at DESC LIMIT").WillReturnError(errors.New("db down"))
		_, _, err := repo.ListByUser(ctx, userID, 1, 10)
		assert.ErrorContains(t, err, "listing user payouts")
	})

	t.Run("row iteration error", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		rows := sqlmock.NewRows(payoutColumns).AddRow(payoutRow(uuid.New(), nil)...).RowError(0, errors.New("broken"))
		mock.ExpectQuery("ORDER BY created_at DESC LIMIT").WillReturnRows(rows)
		_, _, err := repo.ListByUser(ctx, userID, 1, 10)
		assert.Error(t, err)
	})
}

func TestPayoutPG_ListByCircle(t *testing.T) {
	ctx := context.Background()
	circleID := uuid.New()

	t.Run("returns payouts and total", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WithArgs(circleID).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("ORDER BY created_at DESC LIMIT").WithArgs(circleID, 20, 0).
			WillReturnRows(sqlmock.NewRows(payoutColumns).AddRow(payoutRow(uuid.New(), nil)...))

		payouts, total, err := repo.ListByCircle(ctx, circleID, 1, 500)
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Len(t, payouts, 1)
	})

	t.Run("count error", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("db down"))
		_, _, err := repo.ListByCircle(ctx, circleID, 1, 10)
		assert.ErrorContains(t, err, "counting circle payouts")
	})

	t.Run("list error", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("ORDER BY created_at DESC LIMIT").WillReturnError(errors.New("db down"))
		_, _, err := repo.ListByCircle(ctx, circleID, 1, 10)
		assert.ErrorContains(t, err, "listing circle payouts")
	})

	t.Run("scan error while iterating", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("ORDER BY created_at DESC LIMIT").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		_, _, err := repo.ListByCircle(ctx, circleID, 1, 10)
		assert.ErrorContains(t, err, "scanning payout row")
	})

	t.Run("row iteration error", func(t *testing.T) {
		repo, mock := newPayoutRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		rows := sqlmock.NewRows(payoutColumns).AddRow(payoutRow(uuid.New(), nil)...).RowError(0, errors.New("broken"))
		mock.ExpectQuery("ORDER BY created_at DESC LIMIT").WillReturnRows(rows)
		_, _, err := repo.ListByCircle(ctx, circleID, 1, 10)
		assert.ErrorContains(t, err, "iterating payouts")
	})
}

func TestPayoutPG_NewRepositoryFromTxUsesTransaction(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	id := uuid.New()
	mock.ExpectBegin()
	mock.ExpectQuery("FROM payouts WHERE id").WillReturnRows(sqlmock.NewRows(payoutColumns).AddRow(payoutRow(id, nil)...))
	mock.ExpectCommit()

	tx, err := sqlx.NewDb(db, "sqlmock").Beginx()
	require.NoError(t, err)
	repo := NewRepositoryFromTx(tx)

	p, err := repo.FindByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, id, p.ID)
	require.NoError(t, tx.Commit())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIsUniqueViolationPg_Payout(t *testing.T) {
	assert.False(t, isUniqueViolationPg(errors.New("plain")))
	assert.False(t, isUniqueViolationPg(&pq.Error{Code: "23503"}))
	assert.True(t, isUniqueViolationPg(&pq.Error{Code: "23505"}))
}
