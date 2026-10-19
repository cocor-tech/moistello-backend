package admin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/postgres"
)

var dailyVolumeColumns = []string{"day", "contribution_volume", "payout_volume"}

func newAdminRepoMock(t *testing.T) (Repository, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = mockDB.Close() })
	return NewRepositoryWithReader(postgres.NewReader(sqlx.NewDb(mockDB, "sqlmock"), nil)), mock
}

func TestRepository_DailyVolume(t *testing.T) {
	ctx := context.Background()
	day1 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	day2 := day1.AddDate(0, 0, 1)

	t.Run("returns one point per day in the default 30-day window", func(t *testing.T) {
		repo, mock := newAdminRepoMock(t)
		mock.ExpectQuery("generate_series").WithArgs(30).WillReturnRows(
			sqlmock.NewRows(dailyVolumeColumns).AddRow(day1, 100.0, 40.0).AddRow(day2, 0.0, 0.0),
		)

		points, err := repo.DailyVolume(ctx, 0)
		require.NoError(t, err)
		require.Len(t, points, 2)
		assert.Equal(t, day1, points[0].Date)
		assert.Equal(t, 100.0, points[0].ContributionVolume)
		assert.Equal(t, 40.0, points[0].PayoutVolume)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("honors the requested window", func(t *testing.T) {
		repo, mock := newAdminRepoMock(t)
		mock.ExpectQuery("generate_series").WithArgs(45).WillReturnRows(sqlmock.NewRows(dailyVolumeColumns))

		_, err := repo.DailyVolume(ctx, 45)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns a non-nil empty slice when there are no days", func(t *testing.T) {
		repo, mock := newAdminRepoMock(t)
		mock.ExpectQuery("generate_series").WillReturnRows(sqlmock.NewRows(dailyVolumeColumns))

		points, err := repo.DailyVolume(ctx, 7)
		require.NoError(t, err)
		assert.NotNil(t, points)
		assert.Empty(t, points)
	})

	t.Run("wraps query failures", func(t *testing.T) {
		repo, mock := newAdminRepoMock(t)
		boom := errors.New("connection reset")
		mock.ExpectQuery("generate_series").WillReturnError(boom)

		points, err := repo.DailyVolume(ctx, 7)
		assert.Nil(t, points)
		assert.ErrorContains(t, err, "querying daily volume")
		assert.ErrorIs(t, err, boom)
	})

	t.Run("reports rows that fail to scan", func(t *testing.T) {
		repo, mock := newAdminRepoMock(t)
		mock.ExpectQuery("generate_series").WillReturnRows(
			sqlmock.NewRows(dailyVolumeColumns).AddRow(day1, "not-a-number", 0.0),
		)

		points, err := repo.DailyVolume(ctx, 7)
		assert.Nil(t, points)
		assert.ErrorContains(t, err, "scanning daily volume")
	})

	t.Run("reports iteration failures", func(t *testing.T) {
		repo, mock := newAdminRepoMock(t)
		boom := errors.New("conn reset")
		mock.ExpectQuery("generate_series").WillReturnRows(
			sqlmock.NewRows(dailyVolumeColumns).AddRow(day1, 1.0, 1.0).RowError(0, boom),
		)

		points, err := repo.DailyVolume(ctx, 7)
		assert.Nil(t, points)
		assert.ErrorContains(t, err, "iterating daily volume")
		assert.ErrorIs(t, err, boom)
	})
}
