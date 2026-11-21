package circle

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTransactorForTest(t *testing.T) (Transactor, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = mockDB.Close() })
	return NewTransactor(sqlx.NewDb(mockDB, "sqlmock")), mock
}

func TestCircleTransactor_CommitsWhenTheCallbackSucceeds(t *testing.T) {
	tx, mock := newTransactorForTest(t)
	mock.ExpectBegin()
	mock.ExpectCommit()

	var got Repository
	err := tx.WithTransaction(context.Background(), func(repo Repository) error {
		got = repo
		return nil
	})

	require.NoError(t, err)
	assert.NotNil(t, got, "the callback must receive a repository bound to the transaction")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCircleTransactor_RollsBackAndReturnsTheCallbackError(t *testing.T) {
	tx, mock := newTransactorForTest(t)
	mock.ExpectBegin()
	mock.ExpectRollback()
	callbackErr := errors.New("callback failed")

	err := tx.WithTransaction(context.Background(), func(Repository) error { return callbackErr })

	assert.Equal(t, callbackErr, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCircleTransactor_ReturnsABeginErrorWithoutRunningTheCallback(t *testing.T) {
	tx, mock := newTransactorForTest(t)
	beginErr := errors.New("cannot begin")
	mock.ExpectBegin().WillReturnError(beginErr)
	called := false

	err := tx.WithTransaction(context.Background(), func(Repository) error {
		called = true
		return nil
	})

	assert.Equal(t, beginErr, err)
	assert.False(t, called)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCircleTransactor_ReturnsACommitError(t *testing.T) {
	tx, mock := newTransactorForTest(t)
	commitErr := errors.New("cannot commit")
	mock.ExpectBegin()
	mock.ExpectCommit().WillReturnError(commitErr)

	err := tx.WithTransaction(context.Background(), func(Repository) error { return nil })

	assert.Equal(t, commitErr, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCircleTransactor_RollsBackAndRepanicsWhenTheCallbackPanics(t *testing.T) {
	tx, mock := newTransactorForTest(t)
	mock.ExpectBegin()
	mock.ExpectRollback()

	assert.PanicsWithValue(t, "boom", func() {
		_ = tx.WithTransaction(context.Background(), func(Repository) error { panic("boom") })
	})

	assert.NoError(t, mock.ExpectationsWereMet(), "a panicking callback must not leave the transaction open")
}
