package withdrawal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/moistello/backend/pkg/money"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRepo(t *testing.T) (Repository, sqlmock.Sqlmock, func()) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	db := sqlx.NewDb(mockDB, "sqlmock")
	return NewRepository(db), mock, func() { mockDB.Close() }
}

func TestRepository_Create(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	w := &Withdrawal{
		ID:              "wd-1",
		UserID:          "user-1",
		AmountUSDC:      money.MustFromString("100.5"),
		EstimatedNGN:    money.MustFromString("150750.25"),
		BankCode:        "044",
		AccountNumber:   "0123456789",
		AccountName:     "Jane Doe",
		Status:          WithdrawalStatusPending,
		PlatformAddress: "GPLATFORM",
		PaymentRef:      "MOIST-1",
		CreatedAt:       time.Now(),
	}

	mock.ExpectExec("INSERT INTO withdrawals").
		WithArgs(w.ID, w.UserID, w.AmountUSDC, w.EstimatedNGN, w.BankCode, w.AccountNumber,
			w.AccountName, w.Status, w.PlatformAddress, w.PaymentRef, w.CreatedAt).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.Create(context.Background(), w)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func withdrawalRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "user_id", "amount_usdc", "estimated_ngn", "bank_code", "account_number",
		"account_name", "status", "platform_address", "usdc_tx_hash", "yellow_card_tx_id",
		"created_at", "received_at", "completed_at", "failure_reason", "payment_ref",
	}).AddRow("wd-1", "user-1", []byte("100.5000000"), []byte("150750.25"), "044", "0123456789",
		"Jane Doe", WithdrawalStatusPending, "GPLATFORM", nil, nil,
		time.Now(), nil, nil, nil, "MOIST-1")
}

func TestRepository_GetByID(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery("SELECT (.+) FROM withdrawals WHERE id").
		WithArgs("wd-1").
		WillReturnRows(withdrawalRows())

	w, err := repo.GetByID(context.Background(), "wd-1")

	require.NoError(t, err)
	assert.Equal(t, "wd-1", w.ID)
	assert.Equal(t, "100.5000000", w.AmountUSDC.String())
	assert.Equal(t, "150750.2500000", w.EstimatedNGN.String())
}

func TestRepository_GetByPaymentRef(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery("SELECT (.+) FROM withdrawals WHERE payment_ref").
		WithArgs("MOIST-1").
		WillReturnRows(withdrawalRows())

	w, err := repo.GetByPaymentRef(context.Background(), "MOIST-1")

	require.NoError(t, err)
	assert.Equal(t, "MOIST-1", w.PaymentRef)
}

func TestRepository_GetByPaymentRef_NotFound(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery("SELECT (.+) FROM withdrawals WHERE payment_ref").
		WithArgs("missing").
		WillReturnError(errors.New("sql: no rows in result set"))

	w, err := repo.GetByPaymentRef(context.Background(), "missing")

	assert.Error(t, err)
	assert.Nil(t, w)
}

func TestRepository_GetByYellowCardTxID(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery("SELECT (.+) FROM withdrawals WHERE yellow_card_tx_id").
		WithArgs("s-789").
		WillReturnRows(withdrawalRows())

	w, err := repo.GetByYellowCardTxID(context.Background(), "s-789")

	require.NoError(t, err)
	assert.Equal(t, "wd-1", w.ID)
}

func TestRepository_GetByUserID(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectQuery("SELECT (.+) FROM withdrawals WHERE user_id").
		WithArgs("user-1", 10, 0).
		WillReturnRows(withdrawalRows())

	withdrawals, err := repo.GetByUserID(context.Background(), "user-1", 10, 0)

	require.NoError(t, err)
	assert.Len(t, withdrawals, 1)
}

func TestRepository_UpdateStatus(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec("UPDATE withdrawals SET status = \\$1 WHERE id = \\$2").
		WithArgs(WithdrawalStatusProcessing, "wd-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdateStatus(context.Background(), "wd-1", WithdrawalStatusProcessing)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_UpdateUSDCTxHash(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec("UPDATE withdrawals").
		WithArgs("txhash-abc", WithdrawalStatusReceived, sqlmock.AnyArg(), "wd-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdateUSDCTxHash(context.Background(), "wd-1", "txhash-abc", time.Now())

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_UpdateYellowCardTxID(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec("UPDATE withdrawals").
		WithArgs("s-789", WithdrawalStatusConverting, "wd-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdateYellowCardTxID(context.Background(), "wd-1", "s-789")

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_MarkCompleted(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec("UPDATE withdrawals").
		WithArgs(WithdrawalStatusCompleted, sqlmock.AnyArg(), "wd-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.MarkCompleted(context.Background(), "wd-1", time.Now())

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_MarkFailed(t *testing.T) {
	repo, mock, cleanup := newTestRepo(t)
	defer cleanup()

	mock.ExpectExec("UPDATE withdrawals").
		WithArgs(WithdrawalStatusFailed, "bank rejected transfer", "wd-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.MarkFailed(context.Background(), "wd-1", "bank rejected transfer")

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
