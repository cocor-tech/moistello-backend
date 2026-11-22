package contribution_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/pkg/apperrors"
)

var errDB = errors.New("db unavailable")

// newSQLMock returns an sqlx handle backed by sqlmock. Cleanup verifies every
// expectation was consumed.
func newSQLMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return sqlx.NewDb(db, "sqlmock"), mock
}

func TestPgRepo_FindByID_NotFound(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectQuery("FROM contributions WHERE id").WillReturnError(sql.ErrNoRows)

	got, err := contribution.NewRepository(db).FindByID(context.Background(), uuid.New())

	assert.ErrorIs(t, err, apperrors.ErrNotFound)
	assert.Nil(t, got)
}

func TestPgRepo_FindByID_DBError(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectQuery("FROM contributions WHERE id").WillReturnError(errDB)

	got, err := contribution.NewRepository(db).FindByID(context.Background(), uuid.New())

	assert.ErrorIs(t, err, errDB)
	assert.ErrorContains(t, err, "scanning contribution row")
	assert.Nil(t, got)
}

func TestPgRepo_Create_Conflict(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectExec("INSERT INTO contributions").WillReturnError(&pq.Error{Code: "23505"})

	err := contribution.NewRepository(db).Create(context.Background(), &contribution.Contribution{ID: uuid.New()})

	assert.ErrorIs(t, err, apperrors.ErrConflict)
}

func TestPgRepo_Create_DBError(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectExec("INSERT INTO contributions").WillReturnError(errDB)

	err := contribution.NewRepository(db).Create(context.Background(), &contribution.Contribution{ID: uuid.New()})

	assert.ErrorIs(t, err, errDB)
	assert.ErrorContains(t, err, "creating contribution")
}

func TestPgRepo_UpdateStatus_Errors(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := contribution.NewRepository(db)
	id := uuid.New()

	mock.ExpectExec("UPDATE contributions SET status").WillReturnError(errDB)
	err := repo.UpdateStatus(context.Background(), id, contribution.ContributionStatus("confirmed"), "hash")
	assert.ErrorIs(t, err, errDB)

	mock.ExpectExec("UPDATE contributions SET status").WillReturnResult(sqlmock.NewResult(0, 0))
	err = repo.UpdateStatus(context.Background(), id, contribution.ContributionStatus("confirmed"), "hash")
	assert.ErrorIs(t, err, apperrors.ErrNotFound)
}

func TestPgRepo_UpdateVerificationStatus_NotFound(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectExec("UPDATE contributions SET verified_onchain").WillReturnResult(sqlmock.NewResult(0, 0))

	err := contribution.NewRepository(db).UpdateVerificationStatus(context.Background(), uuid.New(), true, contribution.VerificationStatus("verified"))

	assert.ErrorIs(t, err, apperrors.ErrNotFound)
}

func TestPgRepo_ListByUser_CountError(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(errDB)

	items, total, err := contribution.NewRepository(db).ListByUser(context.Background(), uuid.New(), 1, 10)

	assert.ErrorIs(t, err, errDB)
	assert.ErrorContains(t, err, "counting user contributions")
	assert.Nil(t, items)
	assert.Equal(t, 0, total)
}

func TestPgTransactor_BeginError(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectBegin().WillReturnError(errDB)

	err := contribution.NewTransactor(db).WithTransaction(context.Background(), func(repo contribution.Repository) error {
		t.Fatal("callback must not run when begin fails")
		return nil
	})

	assert.ErrorIs(t, err, errDB)
	assert.ErrorContains(t, err, "beginning transaction")
}

func TestPgTransactor_CallbackErrorRollsBack(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectBegin()
	mock.ExpectRollback()
	boom := errors.New("callback failed")

	err := contribution.NewTransactor(db).WithTransaction(context.Background(), func(repo contribution.Repository) error {
		return boom
	})

	assert.ErrorIs(t, err, boom)
}

func TestPgTransactor_CommitsOnSuccess(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectBegin()
	mock.ExpectCommit()

	err := contribution.NewTransactor(db).WithTransaction(context.Background(), func(repo contribution.Repository) error {
		return nil
	})

	assert.NoError(t, err)
}
