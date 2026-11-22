package wallet

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	sqlxDB := sqlx.NewDb(db, "sqlmock")
	t.Cleanup(func() { _ = db.Close() })
	return sqlxDB, mock
}

func TestRepository_Create(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewRepository(db)

	w := &Wallet{
		UserID:             "user-1",
		PublicKey:          "GPUBLIC",
		EncryptedSecretKey: []byte("enc-secret"),
		EncryptionNonce:    []byte("nonce"),
		WalletType:         WalletTypeAuto,
		IsPrimary:          true,
	}

	mock.ExpectQuery(`INSERT INTO wallets`).
		WithArgs(w.UserID, w.PublicKey, w.EncryptedSecretKey, w.EncryptionNonce, w.WalletType, w.IsPrimary).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow("wallet-123", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"))

	err := repo.Create(context.Background(), w)
	require.NoError(t, err)
	assert.Equal(t, "wallet-123", w.ID)
	assert.Equal(t, "2026-01-01T00:00:00Z", w.CreatedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_FindByID(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewRepository(db)

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "public_key", "encrypted_secret_key", "encryption_nonce",
		"wallet_type", "is_primary", "created_at", "updated_at",
	}).AddRow("w-1", "u-1", "GPUB", []byte("enc"), []byte("nonce"), "auto", true, "2026-01-01", "2026-01-01")

	mock.ExpectQuery(`SELECT (.+) FROM wallets WHERE id = \$1`).
		WithArgs("w-1").
		WillReturnRows(rows)

	w, err := repo.FindByID(context.Background(), "w-1")
	require.NoError(t, err)
	assert.Equal(t, "w-1", w.ID)
	assert.Equal(t, "u-1", w.UserID)

	// Error case
	mock.ExpectQuery(`SELECT (.+) FROM wallets WHERE id = \$1`).
		WithArgs("non-existent").
		WillReturnError(sql.ErrNoRows)

	_, err = repo.FindByID(context.Background(), "non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "finding wallet by id")
}

func TestRepository_FindByUserID(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewRepository(db)

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "public_key", "wallet_type", "is_primary", "created_at", "updated_at",
	}).AddRow("w-1", "u-1", "GPUB1", "auto", true, "2026-01-01", "2026-01-01").
		AddRow("w-2", "u-1", "GPUB2", "passkey", false, "2026-01-02", "2026-01-02")

	mock.ExpectQuery(`SELECT (.+) FROM wallets WHERE user_id = \$1`).
		WithArgs("u-1").
		WillReturnRows(rows)

	list, err := repo.FindByUserID(context.Background(), "u-1")
	require.NoError(t, err)
	assert.Len(t, list, 2)

	// Error case
	mock.ExpectQuery(`SELECT (.+) FROM wallets WHERE user_id = \$1`).
		WithArgs("u-err").
		WillReturnError(errors.New("db disconnect"))

	_, err = repo.FindByUserID(context.Background(), "u-err")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "finding wallets by user")
}

func TestRepository_FindByPublicKey(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewRepository(db)

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "public_key", "encrypted_secret_key", "encryption_nonce",
		"wallet_type", "is_primary", "created_at", "updated_at",
	}).AddRow("w-1", "u-1", "GPUB", []byte("enc"), []byte("nonce"), "auto", true, "2026-01-01", "2026-01-01")

	mock.ExpectQuery(`SELECT (.+) FROM wallets WHERE public_key = \$1`).
		WithArgs("GPUB").
		WillReturnRows(rows)

	w, err := repo.FindByPublicKey(context.Background(), "GPUB")
	require.NoError(t, err)
	assert.Equal(t, "GPUB", w.PublicKey)

	// Error case
	mock.ExpectQuery(`SELECT (.+) FROM wallets WHERE public_key = \$1`).
		WithArgs("unknown").
		WillReturnError(sql.ErrNoRows)

	_, err = repo.FindByPublicKey(context.Background(), "unknown")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "finding wallet by public key")
}

func TestRepository_Delete_And_DeleteByOwner(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewRepository(db)

	// Delete
	mock.ExpectExec(`DELETE FROM wallets WHERE id = \$1`).
		WithArgs("w-1").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.Delete(context.Background(), "w-1")
	require.NoError(t, err)

	// DeleteByOwner: success
	mock.ExpectExec(`DELETE FROM wallets WHERE id = \$1 AND user_id = \$2`).
		WithArgs("w-1", "u-1").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = repo.DeleteByOwner(context.Background(), "w-1", "u-1")
	require.NoError(t, err)

	// DeleteByOwner: 0 rows affected
	mock.ExpectExec(`DELETE FROM wallets WHERE id = \$1 AND user_id = \$2`).
		WithArgs("w-2", "u-wrong").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.DeleteByOwner(context.Background(), "w-2", "u-wrong")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "wallet not found or does not belong to user")

	// DeleteByOwner: exec error
	mock.ExpectExec(`DELETE FROM wallets WHERE id = \$1 AND user_id = \$2`).
		WithArgs("w-err", "u-err").
		WillReturnError(errors.New("db error"))

	err = repo.DeleteByOwner(context.Background(), "w-err", "u-err")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deleting wallet")
}

func TestRepository_SecurityMethods(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewRepository(db)
	uID := uuid.New()

	// 1. CheckRateLimit: count < 3
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM withdrawal_rate_limits`).
		WithArgs(uID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	allowed, err := repo.CheckRateLimit(context.Background(), uID)
	require.NoError(t, err)
	assert.True(t, allowed)

	// CheckRateLimit: count >= 3
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM withdrawal_rate_limits`).
		WithArgs(uID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	allowed, err = repo.CheckRateLimit(context.Background(), uID)
	require.NoError(t, err)
	assert.False(t, allowed)

	// CheckRateLimit: error
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM withdrawal_rate_limits`).
		WithArgs(uID).
		WillReturnError(errors.New("rate limit db error"))

	_, err = repo.CheckRateLimit(context.Background(), uID)
	assert.Error(t, err)

	// 2. IncrementRateLimit
	mock.ExpectExec(`INSERT INTO withdrawal_rate_limits`).
		WithArgs(uID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = repo.IncrementRateLimit(context.Background(), uID)
	require.NoError(t, err)

	// 3. GetDailySpending
	// Valid float
	mock.ExpectQuery(`SELECT SUM\(amount\) FROM withdrawal_audit`).
		WithArgs(uID).
		WillReturnRows(sqlmock.NewRows([]string{"total"}).AddRow(125.50))

	spending, err := repo.GetDailySpending(context.Background(), uID)
	require.NoError(t, err)
	assert.Equal(t, 125.50, spending)

	// NULL sum
	mock.ExpectQuery(`SELECT SUM\(amount\) FROM withdrawal_audit`).
		WithArgs(uID).
		WillReturnRows(sqlmock.NewRows([]string{"total"}).AddRow(nil))

	spending, err = repo.GetDailySpending(context.Background(), uID)
	require.NoError(t, err)
	assert.Equal(t, 0.0, spending)

	// Error
	mock.ExpectQuery(`SELECT SUM\(amount\) FROM withdrawal_audit`).
		WithArgs(uID).
		WillReturnError(errors.New("audit db error"))

	_, err = repo.GetDailySpending(context.Background(), uID)
	assert.Error(t, err)

	// 4. RecordWithdrawalAudit
	auditRec := &WithdrawalRecord{
		ID:          uuid.New(),
		UserID:      uID,
		Destination: "GDEST",
		Asset:       "XLM",
		Amount:      50.0,
		Status:      "completed",
		CreatedAt:   time.Now(),
	}
	mock.ExpectExec(`INSERT INTO withdrawal_audit`).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = repo.RecordWithdrawalAudit(context.Background(), auditRec)
	require.NoError(t, err)
}
