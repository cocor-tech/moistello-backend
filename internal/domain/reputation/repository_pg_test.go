package reputation

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

var snapshotColumns = []string{"user_id", "score", "level", "breakdown", "month", "created_at"}

func newReputationRepoMock(t *testing.T) (Repository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewRepository(sqlx.NewDb(db, "sqlmock")), mock
}

func snapshotRow(userID uuid.UUID) []driver.Value {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return []driver.Value{userID.String(), 250, "Silver", []byte(`{"streaks":105}`), now, now}
}

func sampleSnapshot(userID uuid.UUID) *ReputationSnapshot {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return &ReputationSnapshot{
		UserID:    userID,
		Score:     250,
		Level:     "Silver",
		Breakdown: []byte(`{"streaks":105}`),
		Month:     now,
		CreatedAt: now,
	}
}

func TestPGRepo_GetByUser(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("returns latest snapshot", func(t *testing.T) {
		repo, mock := newReputationRepoMock(t)
		mock.ExpectQuery("FROM reputation_snapshots WHERE user_id").WithArgs(userID).
			WillReturnRows(sqlmock.NewRows(snapshotColumns).AddRow(snapshotRow(userID)...))

		s, err := repo.GetByUser(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, userID, s.UserID)
		assert.Equal(t, 250, s.Score)
		assert.JSONEq(t, `{"streaks":105}`, string(s.Breakdown))
	})

	t.Run("no snapshot maps to not found", func(t *testing.T) {
		repo, mock := newReputationRepoMock(t)
		mock.ExpectQuery("FROM reputation_snapshots WHERE user_id").WillReturnRows(sqlmock.NewRows(snapshotColumns))

		s, err := repo.GetByUser(ctx, userID)
		assert.Nil(t, s)
		assert.ErrorIs(t, err, apperrors.ErrNotFound)
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		repo, mock := newReputationRepoMock(t)
		mock.ExpectQuery("FROM reputation_snapshots WHERE user_id").WillReturnError(errors.New("db down"))

		s, err := repo.GetByUser(ctx, userID)
		assert.Nil(t, s)
		assert.ErrorContains(t, err, "scanning reputation snapshot")
	})
}

func TestPGRepo_SaveSnapshot(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("upserts snapshot", func(t *testing.T) {
		repo, mock := newReputationRepoMock(t)
		mock.ExpectExec("ON CONFLICT").WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, repo.SaveSnapshot(ctx, sampleSnapshot(userID)))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unique violation maps to conflict", func(t *testing.T) {
		repo, mock := newReputationRepoMock(t)
		mock.ExpectExec("ON CONFLICT").WillReturnError(&pq.Error{Code: "23505"})

		err := repo.SaveSnapshot(ctx, sampleSnapshot(userID))
		assert.ErrorIs(t, err, apperrors.ErrConflict)
	})

	t.Run("other db error is wrapped", func(t *testing.T) {
		repo, mock := newReputationRepoMock(t)
		mock.ExpectExec("ON CONFLICT").WillReturnError(errors.New("db down"))

		err := repo.SaveSnapshot(ctx, sampleSnapshot(userID))
		assert.ErrorContains(t, err, "saving reputation snapshot")
		assert.NotErrorIs(t, err, apperrors.ErrConflict)
	})
}

func TestPGRepo_GetHistory(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("returns snapshots in order", func(t *testing.T) {
		repo, mock := newReputationRepoMock(t)
		mock.ExpectQuery("FROM reputation_snapshots WHERE user_id").
			WithArgs(userID, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows(snapshotColumns).AddRow(snapshotRow(userID)...).AddRow(snapshotRow(userID)...))

		history, err := repo.GetHistory(ctx, userID, 6)
		require.NoError(t, err)
		assert.Len(t, history, 2)
	})

	t.Run("empty history returns nil slice without error", func(t *testing.T) {
		repo, mock := newReputationRepoMock(t)
		mock.ExpectQuery("FROM reputation_snapshots WHERE user_id").
			WillReturnRows(sqlmock.NewRows(snapshotColumns))

		history, err := repo.GetHistory(ctx, userID, 0)
		require.NoError(t, err)
		assert.Empty(t, history)
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		repo, mock := newReputationRepoMock(t)
		mock.ExpectQuery("FROM reputation_snapshots WHERE user_id").WillReturnError(errors.New("db down"))

		_, err := repo.GetHistory(ctx, userID, 12)
		assert.ErrorContains(t, err, "getting reputation history")
	})

	t.Run("row iteration error is wrapped", func(t *testing.T) {
		repo, mock := newReputationRepoMock(t)
		rows := sqlmock.NewRows(snapshotColumns).AddRow(snapshotRow(userID)...).
			AddRow(snapshotRow(userID)...).RowError(1, errors.New("broken row"))
		mock.ExpectQuery("FROM reputation_snapshots WHERE user_id").WillReturnRows(rows)

		_, err := repo.GetHistory(ctx, userID, 12)
		assert.Error(t, err)
	})
}

func TestIsUniqueViolationPg(t *testing.T) {
	assert.False(t, isUniqueViolationPg(nil))
	assert.False(t, isUniqueViolationPg(errors.New("plain")))
	assert.False(t, isUniqueViolationPg(&pq.Error{Code: "23503"}))
	assert.True(t, isUniqueViolationPg(&pq.Error{Code: "23505"}))
}
