package invite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/apperrors"
)

var inviteColumns = []string{"id", "circle_id", "code", "created_by", "max_uses", "use_count", "expires_at", "created_at"}

func newInviteRepoMock(t *testing.T) (Repository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewRepository(sqlx.NewDb(db, "sqlmock")), mock
}

func inviteRow(expiresAt driver.Value) []driver.Value {
	return []driver.Value{uuid.New(), uuid.New(), "abc123", uuid.New(), 5, 1, expiresAt,
		time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
}

func TestInvitePG_FindByCode(t *testing.T) {
	ctx := context.Background()

	t.Run("success with expiry", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		exp := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
		mock.ExpectQuery("FROM invites WHERE code").WithArgs("abc123").
			WillReturnRows(sqlmock.NewRows(inviteColumns).AddRow(inviteRow(exp)...))

		inv, err := repo.FindByCode(ctx, "abc123")
		require.NoError(t, err)
		assert.Equal(t, "abc123", inv.Code)
		assert.True(t, inv.ExpiresAt.Valid)
		assert.Equal(t, exp, inv.ExpiresAt.Time)
	})

	t.Run("null expiry stays invalid", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectQuery("FROM invites WHERE code").WillReturnRows(sqlmock.NewRows(inviteColumns).AddRow(inviteRow(nil)...))

		inv, err := repo.FindByCode(ctx, "abc123")
		require.NoError(t, err)
		assert.False(t, inv.ExpiresAt.Valid)
	})

	t.Run("missing row is not found", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectQuery("FROM invites WHERE code").WillReturnRows(sqlmock.NewRows(inviteColumns))

		_, err := repo.FindByCode(ctx, "nope")
		assert.ErrorIs(t, err, apperrors.ErrNotFound)
	})

	t.Run("scan failure is wrapped", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectQuery("FROM invites WHERE code").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))

		_, err := repo.FindByCode(ctx, "abc123")
		assert.ErrorContains(t, err, "scanning invite row")
	})
}

func TestInvitePG_FindByCircle(t *testing.T) {
	ctx := context.Background()
	circleID := uuid.New()

	t.Run("returns invites", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectQuery("FROM invites WHERE circle_id").WithArgs(circleID).
			WillReturnRows(sqlmock.NewRows(inviteColumns).AddRow(inviteRow(nil)...).AddRow(inviteRow(nil)...))

		invites, err := repo.FindByCircle(ctx, circleID)
		require.NoError(t, err)
		assert.Len(t, invites, 2)
	})

	t.Run("empty circle returns nil without error", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectQuery("FROM invites WHERE circle_id").WillReturnRows(sqlmock.NewRows(inviteColumns))

		invites, err := repo.FindByCircle(ctx, circleID)
		require.NoError(t, err)
		assert.Empty(t, invites)
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectQuery("FROM invites WHERE circle_id").WillReturnError(errors.New("db down"))

		_, err := repo.FindByCircle(ctx, circleID)
		assert.ErrorContains(t, err, "finding circle invites")
	})

	t.Run("scan error while iterating", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectQuery("FROM invites WHERE circle_id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))

		_, err := repo.FindByCircle(ctx, circleID)
		assert.ErrorContains(t, err, "scanning invite row")
	})

	t.Run("row iteration error", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		rows := sqlmock.NewRows(inviteColumns).AddRow(inviteRow(nil)...).RowError(0, errors.New("broken"))
		mock.ExpectQuery("FROM invites WHERE circle_id").WillReturnRows(rows)

		_, err := repo.FindByCircle(ctx, circleID)
		assert.ErrorContains(t, err, "iterating invites")
	})
}

func TestInvitePG_Create(t *testing.T) {
	ctx := context.Background()
	inv := &Invite{ID: uuid.New(), CircleID: uuid.New(), Code: "abc", MaxUses: 1,
		ExpiresAt: sql.NullTime{Valid: false}, CreatedAt: time.Now().UTC()}

	t.Run("success", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectExec("INSERT INTO invites").WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.Create(ctx, inv))
	})

	t.Run("error is wrapped", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectExec("INSERT INTO invites").WillReturnError(errors.New("db down"))
		assert.ErrorContains(t, repo.Create(ctx, inv), "creating invite")
	})
}

func TestInvitePG_IncrementUse(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectExec("SET use_count = use_count \\+ 1").WithArgs("abc").WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.IncrementUse(ctx, "abc"))
	})

	t.Run("unknown code is not found", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectExec("SET use_count = use_count \\+ 1").WillReturnResult(sqlmock.NewResult(0, 0))
		assert.ErrorIs(t, repo.IncrementUse(ctx, "nope"), apperrors.ErrNotFound)
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectExec("SET use_count = use_count \\+ 1").WillReturnError(errors.New("db down"))
		assert.ErrorContains(t, repo.IncrementUse(ctx, "abc"), "incrementing invite use")
	})
}

func TestInvitePG_Delete(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectExec("DELETE FROM invites WHERE id").WithArgs(id).WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.Delete(ctx, id))
	})

	t.Run("missing invite is not found", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectExec("DELETE FROM invites WHERE id").WillReturnResult(sqlmock.NewResult(0, 0))
		assert.ErrorIs(t, repo.Delete(ctx, id), apperrors.ErrNotFound)
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		repo, mock := newInviteRepoMock(t)
		mock.ExpectExec("DELETE FROM invites WHERE id").WillReturnError(errors.New("db down"))
		assert.ErrorContains(t, repo.Delete(ctx, id), "deleting invite")
	})
}
