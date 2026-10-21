package mobilemoney

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/apperrors"
)

func newTestRepo(t *testing.T) (Repository, sqlmock.Sqlmock, func()) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	db := sqlx.NewDb(mockDB, "sqlmock")
	return NewRepository(db), mock, func() { _ = mockDB.Close() }
}

func rowsFor(t *testing.T) *sqlmock.Rows {
	t.Helper()
	return sqlmock.NewRows([]string{
		"id", "user_id", "provider", "direction", "currency", "amount", "usdc_amount",
		"phone_number", "destination_address", "status", "provider_ref",
		"idempotency_key", "failure_reason", "created_at", "completed_at",
	})
}

func TestRepository_Create(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	txn := &Transaction{
		ID: "txn-1", UserID: "user-1", Provider: "mpesa", Direction: DirectionOnramp,
		Currency: "KES", Amount: 100, USDCAmount: 0.76, PhoneNumber: "254712345678",
		DestinationAddress: "GABC", Status: StatusPending, ProviderRef: "ref-1",
		IdempotencyKey: "key-1", CreatedAt: time.Now(),
	}

	mock.ExpectExec("INSERT INTO mobile_money_transactions").
		WithArgs(txn.ID, txn.UserID, txn.Provider, txn.Direction, txn.Currency, txn.Amount, txn.USDCAmount,
			txn.PhoneNumber, txn.DestinationAddress, txn.Status, txn.ProviderRef, txn.IdempotencyKey, txn.CreatedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, repo.Create(context.Background(), txn))
}

func TestRepository_GetByID_NotFound(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery("SELECT (.+) FROM mobile_money_transactions WHERE id").
		WithArgs("missing").
		WillReturnRows(rowsFor(t))

	_, err := repo.GetByID(context.Background(), "missing")
	assert.ErrorIs(t, err, apperrors.ErrNotFound)
}

func TestRepository_GetByIdempotencyKey_NoneFoundReturnsNilNotError(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery("SELECT (.+) FROM mobile_money_transactions WHERE user_id").
		WithArgs("user-1", "key-1").
		WillReturnRows(rowsFor(t))

	txn, err := repo.GetByIdempotencyKey(context.Background(), "user-1", "key-1")
	require.NoError(t, err)
	assert.Nil(t, txn)
}

func TestRepository_GetByIdempotencyKey_Found(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	rows := rowsFor(t).AddRow("txn-1", "user-1", "mpesa", "onramp", "KES", 100.0, 0.76,
		"254712345678", "GABC", "pending", "ref-1", "key-1", nil, time.Now(), nil)
	mock.ExpectQuery("SELECT (.+) FROM mobile_money_transactions WHERE user_id").
		WithArgs("user-1", "key-1").
		WillReturnRows(rows)

	txn, err := repo.GetByIdempotencyKey(context.Background(), "user-1", "key-1")
	require.NoError(t, err)
	require.NotNil(t, txn)
	assert.Equal(t, "ref-1", txn.ProviderRef)
}

func TestRepository_UpdateStatus(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec("UPDATE mobile_money_transactions").
		WithArgs(StatusCompleted, (*string)(nil), sqlmock.AnyArg(), "txn-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.UpdateStatus(context.Background(), "txn-1", StatusCompleted, nil))
}

func TestRepository_ListPending(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	rows := rowsFor(t).
		AddRow("txn-1", "user-1", "mpesa", "onramp", "KES", 100.0, 0.76,
			"254712345678", "GABC", "pending", "ref-1", "key-1", nil, time.Now(), nil)
	mock.ExpectQuery("SELECT (.+) FROM mobile_money_transactions").
		WithArgs(StatusPending, 30).
		WillReturnRows(rows)

	txns, err := repo.ListPending(context.Background(), 30)
	require.NoError(t, err)
	assert.Len(t, txns, 1)
}
