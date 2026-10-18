package notification

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func newPGTestRepo(t *testing.T) (Repository, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return NewRepository(sqlx.NewDb(sqlDB, "postgres")), mock
}

func TestPGRepositoryOperations(t *testing.T) {
	repo, mock := newPGTestRepo(t)
	ctx := context.Background()
	userID, notificationID := uuid.New(), uuid.New()
	createdAt := time.Now().UTC()
	notification := &Notification{
		ID: notificationID, UserID: userID, Type: TypeCircleCreated,
		Title: "Created", Body: "A circle was created", Data: []byte(`{"circle":"test"}`),
		Channel: ChannelInApp, CreatedAt: createdAt,
	}

	mock.ExpectExec("INSERT INTO notifications").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Create(ctx, notification))

	mock.ExpectQuery("SELECT COUNT").
		WithArgs(userID.String()).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("FROM notifications WHERE user_id").
		WithArgs(userID.String(), 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "type", "title", "body", "data", "is_read", "is_archived", "channel", "created_at",
		}).AddRow(notificationID, userID, TypeCircleCreated, "Created", "A circle was created", []byte(`{"circle":"test"}`), false, false, ChannelInApp, createdAt))

	list, total, err := repo.List(ctx, userID, 0, 0, true)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, list, 1)
	require.Equal(t, notificationID, list[0].ID)

	mock.ExpectExec("UPDATE notifications SET is_read = true WHERE id =").
		WithArgs(notificationID.String(), userID.String()).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkRead(ctx, notificationID, userID))

	mock.ExpectExec("UPDATE notifications SET is_read = true WHERE user_id =").
		WithArgs(userID.String()).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkAllRead(ctx, userID))

	archivedIDs, err := repo.BulkArchive(ctx, userID, nil, true)
	require.NoError(t, err)
	require.Nil(t, archivedIDs)

	mock.ExpectQuery("UPDATE notifications").
		WithArgs(false, userID.String(), notificationID.String()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(notificationID))
	archivedIDs, err = repo.BulkArchive(ctx, userID, []uuid.UUID{notificationID}, false)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{notificationID}, archivedIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}
