package swap

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/apperrors"
)

var swapOfferColumns = []string{
	"id", "circle_id", "offeror_user_id", "offeree_user_id",
	"offeror_asset", "offeror_amount", "requested_asset", "requested_amount",
	"status", "transaction_hash", "expires_at", "created_at", "updated_at",
}

func newSwapRepoMock(t *testing.T) (*PostgresRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresRepository(sqlx.NewDb(db, "sqlmock")), mock
}

func swapOfferRow(id string) []driver.Value {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return []driver.Value{id, "c1", "u1", nil, "XLM", int64(10), "USDC", int64(20),
		"created", nil, now, now, now}
}

func TestSwapPG_CreateSwapOffer(t *testing.T) {
	ctx := context.Background()
	offer := &SwapOffer{ID: "o1", CircleID: "c1", OfferorUserID: "u1", Status: SwapOfferStatusCreated}

	t.Run("success", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("INSERT INTO swap_offers").WillReturnResult(sqlmock.NewResult(0, 1))
		assert.NoError(t, repo.CreateSwapOffer(ctx, offer))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("INSERT INTO swap_offers").WillReturnError(errors.New("db down"))
		assert.Error(t, repo.CreateSwapOffer(ctx, offer))
	})
}

func TestSwapPG_GetSwapOfferByID(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectQuery("FROM swap_offers WHERE id").WithArgs("o1").
			WillReturnRows(sqlmock.NewRows(swapOfferColumns).AddRow(swapOfferRow("o1")...))

		offer, err := repo.GetSwapOfferByID(ctx, "o1")
		require.NoError(t, err)
		assert.Equal(t, "o1", offer.ID)
		assert.Equal(t, SwapOfferStatusCreated, offer.Status)
		assert.Nil(t, offer.OffereeUserID)
	})

	t.Run("missing row maps to not found", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectQuery("FROM swap_offers WHERE id").WillReturnRows(sqlmock.NewRows(swapOfferColumns))

		offer, err := repo.GetSwapOfferByID(ctx, "missing")
		assert.Nil(t, offer)
		assert.ErrorIs(t, err, apperrors.ErrNotFound)
	})

	t.Run("db error is returned", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectQuery("FROM swap_offers WHERE id").WillReturnError(errors.New("db down"))

		_, err := repo.GetSwapOfferByID(ctx, "o1")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, apperrors.ErrNotFound)
	})
}

func TestSwapPG_UpdateSwapOfferStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("UPDATE swap_offers").WillReturnResult(sqlmock.NewResult(0, 1))
		assert.NoError(t, repo.UpdateSwapOfferStatus(ctx, "o1", SwapOfferStatusCancelled, nil))
	})

	t.Run("no matching row is not found", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("UPDATE swap_offers").WillReturnResult(sqlmock.NewResult(0, 0))
		assert.ErrorIs(t, repo.UpdateSwapOfferStatus(ctx, "o1", SwapOfferStatusCancelled, nil), apperrors.ErrNotFound)
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("UPDATE swap_offers").WillReturnError(errors.New("db down"))
		assert.Error(t, repo.UpdateSwapOfferStatus(ctx, "o1", SwapOfferStatusCancelled, nil))
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("UPDATE swap_offers").WillReturnResult(sqlmock.NewErrorResult(errors.New("no rows info")))
		assert.Error(t, repo.UpdateSwapOfferStatus(ctx, "o1", SwapOfferStatusCancelled, nil))
	})
}

func TestSwapPG_CompareAndSwapStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("swapped when row matches expected status", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("UPDATE swap_offers").WillReturnResult(sqlmock.NewResult(0, 1))
		ok, err := repo.CompareAndSwapStatus(ctx, "o1", SwapOfferStatusCreated, SwapOfferStatusAccepted, nil)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("not swapped when status already moved", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("UPDATE swap_offers").WillReturnResult(sqlmock.NewResult(0, 0))
		ok, err := repo.CompareAndSwapStatus(ctx, "o1", SwapOfferStatusCreated, SwapOfferStatusAccepted, nil)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("UPDATE swap_offers").WillReturnError(errors.New("db down"))
		ok, err := repo.CompareAndSwapStatus(ctx, "o1", SwapOfferStatusCreated, SwapOfferStatusAccepted, nil)
		assert.Error(t, err)
		assert.False(t, ok)
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("UPDATE swap_offers").WillReturnResult(sqlmock.NewErrorResult(errors.New("no rows info")))
		ok, err := repo.CompareAndSwapStatus(ctx, "o1", SwapOfferStatusCreated, SwapOfferStatusAccepted, nil)
		assert.Error(t, err)
		assert.False(t, ok)
	})
}

func TestSwapPG_ListUserSwapOffers(t *testing.T) {
	ctx := context.Background()

	t.Run("returns page and total, defaulting limit to 20", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WithArgs("u1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
		mock.ExpectQuery("ORDER BY created_at DESC").WithArgs("u1", 20, 0).
			WillReturnRows(sqlmock.NewRows(swapOfferColumns).AddRow(swapOfferRow("o1")...))

		offers, total, err := repo.ListUserSwapOffers(ctx, "u1", SwapHistoryFilter{})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		require.Len(t, offers, 1)
		assert.Equal(t, "o1", offers[0].ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("honours explicit limit and offset", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WithArgs("u1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))
		mock.ExpectQuery("ORDER BY created_at DESC").WithArgs("u1", 5, 10).
			WillReturnRows(sqlmock.NewRows(swapOfferColumns))

		_, _, err := repo.ListUserSwapOffers(ctx, "u1", SwapHistoryFilter{Limit: 5, Offset: 10})
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("db down"))
		_, total, err := repo.ListUserSwapOffers(ctx, "u1", SwapHistoryFilter{Limit: 5})
		assert.Error(t, err)
		assert.Equal(t, 0, total)
	})

	t.Run("select error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
		mock.ExpectQuery("ORDER BY created_at DESC").WillReturnError(errors.New("db down"))
		offers, total, err := repo.ListUserSwapOffers(ctx, "u1", SwapHistoryFilter{Limit: 5})
		assert.Error(t, err)
		assert.Nil(t, offers)
		assert.Equal(t, 0, total)
	})
}

func TestSwapPG_ListCircleSwapOffers(t *testing.T) {
	ctx := context.Background()

	t.Run("returns page and total", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WithArgs("c1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
		mock.ExpectQuery("ORDER BY created_at DESC").WithArgs("c1", 20, 0).
			WillReturnRows(sqlmock.NewRows(swapOfferColumns).AddRow(swapOfferRow("o1")...))

		offers, total, err := repo.ListCircleSwapOffers(ctx, "c1", SwapHistoryFilter{})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Len(t, offers, 1)
	})

	t.Run("count error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("db down"))
		_, _, err := repo.ListCircleSwapOffers(ctx, "c1", SwapHistoryFilter{Limit: 5})
		assert.Error(t, err)
	})

	t.Run("select error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
		mock.ExpectQuery("ORDER BY created_at DESC").WillReturnError(errors.New("db down"))
		_, _, err := repo.ListCircleSwapOffers(ctx, "c1", SwapHistoryFilter{Limit: 5})
		assert.Error(t, err)
	})
}

func TestSwapPG_ListExpiredCreatedOffers(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectQuery("status = 'created' AND expires_at").WithArgs(now).
			WillReturnRows(sqlmock.NewRows(swapOfferColumns).AddRow(swapOfferRow("o1")...))

		offers, err := repo.ListExpiredCreatedOffers(ctx, now)
		require.NoError(t, err)
		assert.Len(t, offers, 1)
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectQuery("status = 'created' AND expires_at").WillReturnError(errors.New("db down"))
		offers, err := repo.ListExpiredCreatedOffers(ctx, now)
		assert.Error(t, err)
		assert.Nil(t, offers)
	})
}

func TestSwapPG_ClaimOfferForSweep(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	t.Run("claim won", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("SET status = \\$1").WillReturnResult(sqlmock.NewResult(0, 1))
		ok, err := repo.ClaimOfferForSweep(ctx, "o1", now)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("claim lost", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("SET status = \\$1").WillReturnResult(sqlmock.NewResult(0, 0))
		ok, err := repo.ClaimOfferForSweep(ctx, "o1", now)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("SET status = \\$1").WillReturnError(errors.New("db down"))
		ok, err := repo.ClaimOfferForSweep(ctx, "o1", now)
		assert.Error(t, err)
		assert.False(t, ok)
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("SET status = \\$1").WillReturnResult(sqlmock.NewErrorResult(errors.New("no rows info")))
		ok, err := repo.ClaimOfferForSweep(ctx, "o1", now)
		assert.Error(t, err)
		assert.False(t, ok)
	})
}

func TestSwapPG_ReleaseSweepClaim(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("WHERE id = \\$2 AND status = \\$3").WillReturnResult(sqlmock.NewResult(0, 1))
		assert.NoError(t, repo.ReleaseSweepClaim(ctx, "o1"))
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("WHERE id = \\$2 AND status = \\$3").WillReturnError(errors.New("db down"))
		assert.Error(t, repo.ReleaseSweepClaim(ctx, "o1"))
	})
}

func TestSwapPG_FinalizeSweep(t *testing.T) {
	ctx := context.Background()
	tx := "tx-1"

	t.Run("success", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("SET status = \\$1").WillReturnResult(sqlmock.NewResult(0, 1))
		assert.NoError(t, repo.FinalizeSweep(ctx, "o1", &tx))
	})

	t.Run("not sweeping anymore is not found", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("SET status = \\$1").WillReturnResult(sqlmock.NewResult(0, 0))
		assert.ErrorIs(t, repo.FinalizeSweep(ctx, "o1", &tx), apperrors.ErrNotFound)
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("SET status = \\$1").WillReturnError(errors.New("db down"))
		assert.Error(t, repo.FinalizeSweep(ctx, "o1", &tx))
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock := newSwapRepoMock(t)
		mock.ExpectExec("SET status = \\$1").WillReturnResult(sqlmock.NewErrorResult(errors.New("no rows info")))
		assert.Error(t, repo.FinalizeSweep(ctx, "o1", &tx))
	})
}
