package user

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/apperrors"
)

func TestPgRepo_Create(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	u := &User{
		ID:            uuid.New(),
		WalletAddress: "GABC123",
	}

	t.Run("success", func(t *testing.T) {
		mock.ExpectExec("INSERT INTO users").
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.Create(ctx, u)
		assert.NoError(t, err)
	})

	t.Run("conflict error", func(t *testing.T) {
		mock.ExpectExec("INSERT INTO users").
			WillReturnError(&pq.Error{Code: "23505"})

		err := repo.Create(ctx, u)
		assert.ErrorIs(t, err, apperrors.ErrConflict)
	})

	t.Run("generic error", func(t *testing.T) {
		mock.ExpectExec("INSERT INTO users").
			WillReturnError(errors.New("db connection lost"))

		err := repo.Create(ctx, u)
		assert.Error(t, err)
		assert.False(t, errors.Is(err, apperrors.ErrConflict))
	})
}

func TestPgRepo_Update(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	u := &User{
		ID:            uuid.New(),
		WalletAddress: "GABC123",
	}

	t.Run("success", func(t *testing.T) {
		mock.ExpectExec("UPDATE users SET").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Update(ctx, u)
		assert.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		mock.ExpectExec("UPDATE users SET").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Update(ctx, u)
		assert.ErrorIs(t, err, ErrUserNotFound)
	})

	t.Run("exec error", func(t *testing.T) {
		mock.ExpectExec("UPDATE users SET").
			WillReturnError(errors.New("db error"))

		err := repo.Update(ctx, u)
		assert.Error(t, err)
	})
}

func TestPgRepo_UpdateMoiScore(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	id := uuid.New()

	t.Run("success", func(t *testing.T) {
		mock.ExpectExec("UPDATE users SET moi_score = \\$1, updated_at = NOW\\(\\) WHERE id = \\$2").
			WithArgs(500, id).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateMoiScore(ctx, id, 500)
		assert.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		mock.ExpectExec("UPDATE users SET moi_score = \\$1, updated_at = NOW\\(\\) WHERE id = \\$2").
			WithArgs(500, id).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdateMoiScore(ctx, id, 500)
		assert.ErrorIs(t, err, ErrUserNotFound)
	})

	t.Run("exec error", func(t *testing.T) {
		mock.ExpectExec("UPDATE users SET moi_score = \\$1, updated_at = NOW\\(\\) WHERE id = \\$2").
			WithArgs(500, id).
			WillReturnError(errors.New("db error"))

		err := repo.UpdateMoiScore(ctx, id, 500)
		assert.Error(t, err)
	})
}

func TestPgRepo_ClaimNextName(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mock.ExpectQuery("UPDATE user_name_counter SET value = value \\+ 1 WHERE id = 1 RETURNING value").
			WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(42))

		val, err := repo.ClaimNextName(ctx)
		assert.NoError(t, err)
		assert.Equal(t, int64(42), val)
	})

	t.Run("error", func(t *testing.T) {
		mock.ExpectQuery("UPDATE user_name_counter SET value = value \\+ 1 WHERE id = 1 RETURNING value").
			WillReturnError(errors.New("table locked"))

		val, err := repo.ClaimNextName(ctx)
		assert.Error(t, err)
		assert.Equal(t, int64(0), val)
	})
}

func TestPgRepo_ListAndCount_WithSearchAndErrors(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("list with search", func(t *testing.T) {
		mock.ExpectQuery("FROM users WHERE deleted_at IS NULL AND \\(display_name ILIKE \\$1 OR wallet_address ILIKE \\$2 OR email ILIKE \\$3\\)").
			WithArgs("%alice%", "%alice%", "%alice%", 10, 0).
			WillReturnRows(userRows(nil))

		users, err := repo.List(ctx, UserFilter{Page: 1, Limit: 10, Search: "alice"})
		require.NoError(t, err)
		assert.Len(t, users, 1)
	})

	t.Run("list query error", func(t *testing.T) {
		mock.ExpectQuery("FROM users WHERE deleted_at IS NULL").
			WillReturnError(errors.New("query err"))

		_, err := repo.List(ctx, UserFilter{})
		assert.Error(t, err)
	})

	t.Run("count with search", func(t *testing.T) {
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM users WHERE deleted_at IS NULL AND \\(display_name ILIKE \\$1 OR wallet_address ILIKE \\$1 OR email ILIKE \\$1\\)").
			WithArgs("%alice%").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

		count, err := repo.Count(ctx, UserFilter{Search: "alice"})
		require.NoError(t, err)
		assert.Equal(t, 3, count)
	})

	t.Run("count error", func(t *testing.T) {
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM users WHERE deleted_at IS NULL").
			WillReturnError(errors.New("count err"))

		_, err := repo.Count(ctx, UserFilter{})
		assert.Error(t, err)
	})
}

func TestPgRepo_ListAndCountDeleted_WithSearchAndErrors(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("list deleted with search", func(t *testing.T) {
		mock.ExpectQuery("FROM users WHERE deleted_at IS NOT NULL AND \\(display_name ILIKE \\$1 OR wallet_address ILIKE \\$2 OR email ILIKE \\$3\\)").
			WithArgs("%bob%", "%bob%", "%bob%", 20, 20).
			WillReturnRows(userRows(nil))

		users, err := repo.ListDeleted(ctx, UserFilter{Page: 2, Limit: 20, Search: "bob"})
		require.NoError(t, err)
		assert.Len(t, users, 1)
	})

	t.Run("list deleted query error", func(t *testing.T) {
		mock.ExpectQuery("FROM users WHERE deleted_at IS NOT NULL").
			WillReturnError(errors.New("list del err"))

		_, err := repo.ListDeleted(ctx, UserFilter{})
		assert.Error(t, err)
	})

	t.Run("count deleted with search", func(t *testing.T) {
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM users WHERE deleted_at IS NOT NULL AND \\(display_name ILIKE \\$1 OR wallet_address ILIKE \\$1 OR email ILIKE \\$1\\)").
			WithArgs("%bob%").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

		count, err := repo.CountDeleted(ctx, UserFilter{Search: "bob"})
		require.NoError(t, err)
		assert.Equal(t, 2, count)
	})

	t.Run("count deleted error", func(t *testing.T) {
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM users WHERE deleted_at IS NOT NULL").
			WillReturnError(errors.New("count del err"))

		_, err := repo.CountDeleted(ctx, UserFilter{})
		assert.Error(t, err)
	})
}

func TestPgRepo_Delete_ExecError(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	id := uuid.New()
	mock.ExpectExec("UPDATE users SET deleted_at = NOW\\(\\)").
		WithArgs(id).
		WillReturnError(errors.New("db timeout"))

	err := repo.Delete(context.Background(), id)
	assert.Error(t, err)
}

func TestIsUniqueViolation(t *testing.T) {
	assert.False(t, isUniqueViolation(nil))
	assert.False(t, isUniqueViolation(errors.New("some error")))
	assert.False(t, isUniqueViolation(&pq.Error{Code: "12345"}))
	assert.True(t, isUniqueViolation(&pq.Error{Code: "23505"}))
}
