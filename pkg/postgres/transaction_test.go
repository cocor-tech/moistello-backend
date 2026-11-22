package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTxDB returns a sqlx.DB backed by go-sqlmock.
func newTxDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	return sqlx.NewDb(raw, "sqlmock"), mock
}

// --- WithTransaction ---

func TestWithTransaction_CommitsOnSuccess(t *testing.T) {
	db, mock := newTxDB(t)
	mock.ExpectBegin()
	mock.ExpectCommit()

	err := WithTransaction(context.Background(), db, func(tx *sqlx.Tx) error {
		return nil
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWithTransaction_RollsBackOnFnError(t *testing.T) {
	db, mock := newTxDB(t)
	sentinel := errors.New("domain error")
	mock.ExpectBegin()
	mock.ExpectRollback()

	err := WithTransaction(context.Background(), db, func(tx *sqlx.Tx) error {
		return sentinel
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWithTransaction_ReturnsErrorWhenBeginFails(t *testing.T) {
	db, mock := newTxDB(t)
	beginErr := errors.New("cannot begin transaction")
	mock.ExpectBegin().WillReturnError(beginErr)

	err := WithTransaction(context.Background(), db, func(tx *sqlx.Tx) error {
		return nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, beginErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- WithTransactionLevel ---

func TestWithTransactionLevel_SerializableCommits(t *testing.T) {
	db, mock := newTxDB(t)
	mock.ExpectBegin()
	mock.ExpectCommit()

	err := WithTransactionLevel(context.Background(), db, sql.LevelSerializable, func(tx *sqlx.Tx) error {
		return nil
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWithTransactionLevel_RollsBackOnError(t *testing.T) {
	db, mock := newTxDB(t)
	sentinel := errors.New("serializable conflict")
	mock.ExpectBegin()
	mock.ExpectRollback()

	err := WithTransactionLevel(context.Background(), db, sql.LevelSerializable, func(tx *sqlx.Tx) error {
		return sentinel
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Transactor ---

func TestTransactor_WithTransaction_CommitsOnSuccess(t *testing.T) {
	db, mock := newTxDB(t)
	mock.ExpectBegin()
	mock.ExpectCommit()

	tr := NewTransactor(db)
	err := tr.WithTransaction(context.Background(), func() error { return nil })

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactor_WithTransaction_RollsBackOnError(t *testing.T) {
	db, mock := newTxDB(t)
	sentinel := errors.New("transactor error")
	mock.ExpectBegin()
	mock.ExpectRollback()

	tr := NewTransactor(db)
	err := tr.WithTransaction(context.Background(), func() error { return sentinel })

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNewTransactor_SetsDB(t *testing.T) {
	db, _ := newTxDB(t)
	tr := NewTransactor(db)
	assert.Same(t, db, tr.DB)
}
