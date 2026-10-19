package deposit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRepo(t *testing.T) (Repository, sqlmock.Sqlmock, func()) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	db := sqlx.NewDb(mockDB, "sqlmock")
	// The mock's Close error is intentionally ignored: sqlmock reports it for
	// any unexpected Close, and each test asserts its own expectations.
	return NewRepository(db), mock, func() { _ = mockDB.Close() }
}

func TestRepository_Create(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	d := &Deposit{
		ID:                 "dep-1",
		UserID:             "user-1",
		AmountNGN:          50000,
		EstimatedUSDC:      33.33,
		DestinationAddress: "GABC123",
		Status:             DepositStatusPending,
		ReceiveID:          "r-1",
		PaymentRef:         "MOIST-1",
		CreatedAt:          time.Now(),
	}

	mock.ExpectExec("INSERT INTO deposits").
		WithArgs(d.ID, d.UserID, d.AmountNGN, d.EstimatedUSDC, d.DestinationAddress, d.Status,
			d.ReceiveID, d.PaymentRef, d.CreatedAt, d.ExpiresAt).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.Create(context.Background(), d)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_GetByPaymentRef(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "amount_ngn", "estimated_usdc", "destination_address", "status",
		"receive_id", "payment_ref", "created_at", "completed_at", "expires_at", "failure_reason",
	}).AddRow("dep-1", "user-1", 50000.0, 33.33, "GABC123", DepositStatusPending,
		"r-1", "MOIST-1", time.Now(), nil, nil, nil)

	mock.ExpectQuery("SELECT (.+) FROM deposits WHERE payment_ref").
		WithArgs("MOIST-1").
		WillReturnRows(rows)

	d, err := repo.GetByPaymentRef(context.Background(), "MOIST-1")

	require.NoError(t, err)
	assert.Equal(t, "dep-1", d.ID)
	assert.Equal(t, DepositStatusPending, d.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_GetByPaymentRef_NotFound(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery("SELECT (.+) FROM deposits WHERE payment_ref").
		WithArgs("missing").
		WillReturnError(errors.New("sql: no rows in result set"))

	d, err := repo.GetByPaymentRef(context.Background(), "missing")

	assert.Error(t, err)
	assert.Nil(t, d)
}

func TestRepository_GetByReceiveID(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "amount_ngn", "estimated_usdc", "destination_address", "status",
		"receive_id", "payment_ref", "created_at", "completed_at", "expires_at", "failure_reason",
	}).AddRow("dep-1", "user-1", 50000.0, 33.33, "GABC123", DepositStatusPending,
		"r-1", "MOIST-1", time.Now(), nil, nil, nil)

	mock.ExpectQuery("SELECT (.+) FROM deposits WHERE receive_id").
		WithArgs("r-1").
		WillReturnRows(rows)

	d, err := repo.GetByReceiveID(context.Background(), "r-1")

	require.NoError(t, err)
	assert.Equal(t, "r-1", d.ReceiveID)
}

func TestRepository_MarkCompleted(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec("UPDATE deposits SET status = \\$1, completed_at = \\$2").
		WithArgs(DepositStatusCompleted, sqlmock.AnyArg(), "dep-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.MarkCompleted(context.Background(), "dep-1", time.Now())

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_MarkFailed(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec("UPDATE deposits SET status = \\$1, failure_reason = \\$2").
		WithArgs(DepositStatusFailed, "bank transfer never arrived", "dep-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.MarkFailed(context.Background(), "dep-1", "bank transfer never arrived")

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_UpdateStatus(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec("UPDATE deposits SET status = \\$1 WHERE id = \\$2").
		WithArgs(DepositStatusProcessing, "dep-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdateStatus(context.Background(), "dep-1", DepositStatusProcessing)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_GetByUserID(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "amount_ngn", "estimated_usdc", "destination_address", "status",
		"receive_id", "payment_ref", "created_at", "completed_at", "expires_at", "failure_reason",
	}).AddRow("dep-1", "user-1", 50000.0, 33.33, "GABC123", DepositStatusPending,
		"r-1", "MOIST-1", time.Now(), nil, nil, nil)

	mock.ExpectQuery("SELECT (.+) FROM deposits WHERE user_id").
		WithArgs("user-1", 10, 0).
		WillReturnRows(rows)

	deposits, err := repo.GetByUserID(context.Background(), "user-1", 10, 0)

	require.NoError(t, err)
	assert.Len(t, deposits, 1)
}
