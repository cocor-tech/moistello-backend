package user

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRepo(t *testing.T) (Repository, sqlmock.Sqlmock, func()) {
	t.Helper()
	mockDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	db := sqlx.NewDb(mockDB, "sqlmock")
	return NewRepository(db), mock, func() { _ = db.Close() }
}

// userRows builds a row matching the userColumns projection, with deleted_at
// last so the scan order in scanUser is exercised.
func userRows(deletedAt interface{}) *sqlmock.Rows {
	var deleted interface{}
	switch v := deletedAt.(type) {
	case nil:
		deleted = nil
	case time.Time:
		deleted = v
	}
	return sqlmock.NewRows([]string{
		"id", "wallet_address", "email", "phone", "display_name", "avatar_ipfs_hash",
		"country_code", "preferred_language", "moi_score", "role",
		"session_ttl_minutes", "password_hash", "totp_secret", "totp_enabled",
		"backup_codes", "email_verified", "passkey_credential_id",
		"notification_channels", "notifications_muted", "digest_enabled", "digest_interval_minutes", "push_token",
		"created_at", "updated_at", "deleted_at",
	}).AddRow(
		uuid.New(), "GABC", "a@b.c", nil, "Ada", nil,
		"NG", "en", 100, "user",
		60, "hash", nil, false,
		nil, true, nil,
		nil, false, false, 1440, nil,
		time.Now(), time.Now(), deleted,
	)
}
// ---------------------------------------------------------------------------
// Acceptance: soft-deleted users are excluded from auth and normal queries
// ---------------------------------------------------------------------------

// Every lookup used by authentication must filter on deleted_at IS NULL, so a
// soft-deleted account cannot sign in again by any route.
func TestFindMethods_ExcludeSoftDeletedUsers(t *testing.T) {
	cases := []struct {
		name  string
		repo  func(Repository, context.Context) (*User, error)
		query string
	}{
		{"FindByID", func(r Repository, ctx context.Context) (*User, error) {
			return r.FindByID(ctx, uuid.New())
		}, "FROM users WHERE id = .* AND deleted_at IS NULL"},
		{"FindByWalletAddress", func(r Repository, ctx context.Context) (*User, error) {
			return r.FindByWalletAddress(ctx, "GABC")
		}, "FROM users WHERE wallet_address = .* AND deleted_at IS NULL"},
		{"FindByEmail", func(r Repository, ctx context.Context) (*User, error) {
			return r.FindByEmail(ctx, "a@b.c")
		}, "FROM users WHERE email = .* AND deleted_at IS NULL"},
		{"FindByPasskeyCredentialID", func(r Repository, ctx context.Context) (*User, error) {
			return r.FindByPasskeyCredentialID(ctx, "cred-1")
		}, "FROM users WHERE passkey_credential_id = .* AND deleted_at IS NULL"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, mock, cleanup := newTestRepo(t)
			defer cleanup()

			// No rows: the soft-deleted user is invisible to this lookup.
			mock.ExpectQuery(tc.query).
				WillReturnRows(sqlmock.NewRows([]string{"id"}))

			_, err := tc.repo(repo, context.Background())
			require.Error(t, err, "%s must not return a soft-deleted user", tc.name)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestListAndCount_ExcludeSoftDeletedUsers(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery("FROM users WHERE deleted_at IS NULL").
		WillReturnRows(userRows(nil))
	mock.ExpectQuery("SELECT COUNT..* FROM users WHERE deleted_at IS NULL").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	users, err := repo.List(context.Background(), UserFilter{})
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Nil(t, users[0].DeletedAt, "an active user must not carry a deletion stamp")

	total, err := repo.Count(context.Background(), UserFilter{})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Soft delete and restore
// ---------------------------------------------------------------------------

func TestDelete_SoftDeletesRatherThanRemovingRow(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	id := uuid.New()
	// An UPDATE, never a DELETE: the row and its audit trail must survive.
	mock.ExpectExec("UPDATE users SET deleted_at = NOW\\(\\)").
		WithArgs(id).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.Delete(context.Background(), id))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRestore_ClearsDeletedAt(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	id := uuid.New()
	mock.ExpectExec("UPDATE users SET deleted_at = NULL").
		WithArgs(id).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.Restore(context.Background(), id))
	require.NoError(t, mock.ExpectationsWereMet())
}

// A restore aimed at a user who is already active must not report success.
func TestRestore_AlreadyActiveReportsNotDeleted(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	id := uuid.New()
	mock.ExpectExec("UPDATE users SET deleted_at = NULL").
		WithArgs(id).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT deleted_at FROM users WHERE id = ").
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"deleted_at"}).AddRow(nil))

	err := repo.Restore(context.Background(), id)
	require.ErrorIs(t, err, ErrUserNotDeleted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRestore_UnknownUserReportsNotFound(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	id := uuid.New()
	mock.ExpectExec("UPDATE users SET deleted_at = NULL").
		WithArgs(id).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT deleted_at FROM users WHERE id = ").
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"deleted_at"}))

	err := repo.Restore(context.Background(), id)
	require.ErrorIs(t, err, ErrUserNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Repeated deletes must not be mistaken for deleting a live account.
func TestDelete_AlreadyDeletedReportsNotDeleted(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	id := uuid.New()
	mock.ExpectExec("UPDATE users SET deleted_at = NOW\\(\\)").
		WithArgs(id).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT deleted_at FROM users WHERE id = ").
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"deleted_at"}).AddRow(time.Now()))

	err := repo.Delete(context.Background(), id)
	require.ErrorIs(t, err, ErrUserNotDeleted)
	require.NoError(t, mock.ExpectationsWereMet())
}
// ---------------------------------------------------------------------------
// Administrative views over soft-deleted users
// ---------------------------------------------------------------------------

// The admin read path bypasses the filter but still reports the deletion stamp,
// which is what makes the audit trail legible.
func TestFindByIDIncludingDeleted_ReturnsDeletedUserWithTimestamp(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	deletedAt := time.Now().Add(-24 * time.Hour)
	mock.ExpectQuery("FROM users WHERE id = \\$1$").
		WillReturnRows(userRows(deletedAt))

	u, err := repo.FindByIDIncludingDeleted(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, u.DeletedAt, "an admin must be able to see when the user was deleted")
	assert.WithinDuration(t, deletedAt, *u.DeletedAt, time.Second)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListDeleted_ReturnsOnlyDeletedUsers(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery("FROM users WHERE deleted_at IS NOT NULL").
		WillReturnRows(userRows(time.Now()))
	mock.ExpectQuery("SELECT COUNT..* FROM users WHERE deleted_at IS NOT NULL").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))

	users, err := repo.ListDeleted(context.Background(), UserFilter{})
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.NotNil(t, users[0].DeletedAt)

	total, err := repo.CountDeleted(context.Background(), UserFilter{})
	require.NoError(t, err)
	assert.Equal(t, 7, total)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Projection safety
// ---------------------------------------------------------------------------

// Every read path must select deleted_at, otherwise scanUser's column order
// does not line up and the scan fails at runtime. This asserts the shared
// projection is what all lookups use.
func TestAllLookupsUseSharedProjection(t *testing.T) {
	assert.Contains(t, userColumns, "deleted_at")
	assert.Contains(t, userColumns, "notification_channels")
	assert.Contains(t, userColumns, "push_token")
	// #415 added the digest cadence to the shared projection; it must stay in
	// step with the scan targets in scanUser.
	assert.Contains(t, userColumns, "digest_enabled")
	assert.Contains(t, userColumns, "digest_interval_minutes")
	assert.Equal(t, 25, len(splitColumns(userColumns)),
		"userColumns must stay in step with the 25 scan targets in scanUser")
}

func splitColumns(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}