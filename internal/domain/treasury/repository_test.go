package treasury

import (
	"context"
	"database/sql"
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
	return NewRepository(db), mock, func() { _ = mockDB.Close() }
}

func TestGetBalance(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(mock sqlmock.Sqlmock)
		wantXLM   string
		wantUSDC  string
		wantErr   bool
	}{
		{
			name: "success with balances",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"xlm", "usdc"}).AddRow("15000000", "50000000")
				mock.ExpectQuery("SELECT (.+) FROM treasury_transactions WHERE type IN").
					WillReturnRows(rows)
			},
			wantXLM:  "15000000",
			wantUSDC: "50000000",
			wantErr:  false,
		},
		{
			name: "db error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT (.+) FROM treasury_transactions WHERE type IN").
					WillReturnError(errors.New("connection reset"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock, cleanup := newTestRepo(t)
			defer cleanup()

			tt.setupMock(mock)

			balance, err := repo.GetBalance(context.Background())
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, balance)
			} else {
				require.NoError(t, err)
				require.NotNil(t, balance)
				assert.Equal(t, tt.wantXLM, balance.XLM)
				assert.Equal(t, tt.wantUSDC, balance.USDC)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCreateWithdrawalRequest(t *testing.T) {
	now := time.Now().UTC()
	expiry := now.Add(24 * time.Hour)

	req := &WithdrawalRequest{
		ID:             "req-1",
		Destination:    "GDEST12345",
		Asset:          "USDC",
		Amount:         1000000,
		Status:         WithdrawalStatusPending,
		ApprovalMethod: ApprovalMethodTimelock,
		RequestedBy:    "user-1",
		RequestedAt:    now,
		TimelockExpiry: &expiry,
	}

	tests := []struct {
		name      string
		setupMock func(mock sqlmock.Sqlmock)
		wantErr   bool
	}{
		{
			name: "success",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO treasury_withdrawal_requests").
					WithArgs(req.ID, req.Destination, req.Asset, req.Amount, req.Status, req.ApprovalMethod,
						req.RequestedBy, req.RequestedAt, req.TimelockExpiry, req.MultiSigSignatures).
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
			wantErr: false,
		},
		{
			name: "db error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO treasury_withdrawal_requests").
					WithArgs(req.ID, req.Destination, req.Asset, req.Amount, req.Status, req.ApprovalMethod,
						req.RequestedBy, req.RequestedAt, req.TimelockExpiry, req.MultiSigSignatures).
					WillReturnError(errors.New("insert failed"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock, cleanup := newTestRepo(t)
			defer cleanup()

			tt.setupMock(mock)

			err := repo.CreateWithdrawalRequest(context.Background(), req)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func withdrawalRequestColumns() []string {
	return []string{
		"id", "destination", "asset", "amount", "status", "approval_method", "requested_by", "requested_at",
		"approved_by", "approved_at", "tx_hash", "failure_reason", "executed_at", "timelock_expiry", "multi_sig_signatures",
	}
}

func TestGetWithdrawalRequest(t *testing.T) {
	now := time.Now().UTC()
	approvedBy := "admin-1"
	txHash := "tx-hash-123"

	tests := []struct {
		name      string
		id        string
		setupMock func(mock sqlmock.Sqlmock)
		wantReq   *WithdrawalRequest
		wantErr   bool
	}{
		{
			name: "found request",
			id:   "req-1",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows(withdrawalRequestColumns()).AddRow(
					"req-1", "GDEST", "USDC", int64(500), WithdrawalStatusApproved, ApprovalMethodMultiSig,
					"user-1", now, &approvedBy, &now, &txHash, nil, nil, nil, nil,
				)
				mock.ExpectQuery("SELECT (.+) FROM treasury_withdrawal_requests WHERE id = \\$1").
					WithArgs("req-1").
					WillReturnRows(rows)
			},
			wantReq: &WithdrawalRequest{
				ID:             "req-1",
				Destination:    "GDEST",
				Asset:          "USDC",
				Amount:         500,
				Status:         WithdrawalStatusApproved,
				ApprovalMethod: ApprovalMethodMultiSig,
				RequestedBy:    "user-1",
				RequestedAt:    now,
				ApprovedBy:     &approvedBy,
				ApprovedAt:     &now,
				TxHash:         &txHash,
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   "nonexistent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT (.+) FROM treasury_withdrawal_requests WHERE id = \\$1").
					WithArgs("nonexistent").
					WillReturnError(sql.ErrNoRows)
			},
			wantReq: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock, cleanup := newTestRepo(t)
			defer cleanup()

			tt.setupMock(mock)

			res, err := repo.GetWithdrawalRequest(context.Background(), tt.id)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, res)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantReq.ID, res.ID)
				assert.Equal(t, tt.wantReq.Destination, res.Destination)
				assert.Equal(t, tt.wantReq.Status, res.Status)
				assert.Equal(t, tt.wantReq.Amount, res.Amount)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestListWithdrawalRequests(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name      string
		status    WithdrawalStatus
		limit     int
		setupMock func(mock sqlmock.Sqlmock)
		wantCount int
		wantErr   bool
	}{
		{
			name:   "filter by status",
			status: WithdrawalStatusPending,
			limit:  10,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows(withdrawalRequestColumns()).
					AddRow("req-1", "GDEST1", "XLM", int64(100), WithdrawalStatusPending, ApprovalMethodTimelock, "user-1", now, nil, nil, nil, nil, nil, nil, nil).
					AddRow("req-2", "GDEST2", "USDC", int64(200), WithdrawalStatusPending, ApprovalMethodMultiSig, "user-2", now, nil, nil, nil, nil, nil, nil, nil)
				mock.ExpectQuery("SELECT (.+) FROM treasury_withdrawal_requests WHERE status = \\$1 ORDER BY requested_at DESC LIMIT \\$2").
					WithArgs(WithdrawalStatusPending, 10).
					WillReturnRows(rows)
			},
			wantCount: 2,
			wantErr:   false,
		},
		{
			name:   "no status filter",
			status: "",
			limit:  5,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows(withdrawalRequestColumns()).
					AddRow("req-1", "GDEST1", "XLM", int64(100), WithdrawalStatusCompleted, ApprovalMethodTimelock, "user-1", now, nil, nil, nil, nil, nil, nil, nil)
				mock.ExpectQuery("SELECT (.+) FROM treasury_withdrawal_requests ORDER BY requested_at DESC LIMIT \\$1").
					WithArgs(5).
					WillReturnRows(rows)
			},
			wantCount: 1,
			wantErr:   false,
		},
		{
			name:   "query error",
			status: WithdrawalStatusPending,
			limit:  10,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT (.+) FROM treasury_withdrawal_requests WHERE status = \\$1").
					WithArgs(WithdrawalStatusPending, 10).
					WillReturnError(errors.New("db error"))
			},
			wantCount: 0,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock, cleanup := newTestRepo(t)
			defer cleanup()

			tt.setupMock(mock)

			res, err := repo.ListWithdrawalRequests(context.Background(), tt.status, tt.limit)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, res)
			} else {
				require.NoError(t, err)
				assert.Len(t, res, tt.wantCount)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpdateWithdrawalRequest(t *testing.T) {
	tests := []struct {
		name          string
		id            string
		status        WithdrawalStatus
		approvedBy    string
		txHash        string
		failureReason string
		setupMock     func(mock sqlmock.Sqlmock)
		wantErr       bool
	}{
		{
			name:          "success update approved",
			id:            "req-1",
			status:        WithdrawalStatusApproved,
			approvedBy:    "admin-1",
			txHash:        "tx-123",
			failureReason: "",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("UPDATE treasury_withdrawal_requests SET status = \\$1, approved_by = \\$2, approved_at = \\$3, tx_hash = \\$4, failure_reason = \\$5, executed_at = \\$6 WHERE id = \\$7").
					WithArgs(WithdrawalStatusApproved, "admin-1", sqlmock.AnyArg(), "tx-123", "", sqlmock.AnyArg(), "req-1").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name:          "db update error",
			id:            "req-2",
			status:        WithdrawalStatusFailed,
			approvedBy:    "admin-1",
			txHash:        "",
			failureReason: "funds insufficient",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("UPDATE treasury_withdrawal_requests").
					WithArgs(WithdrawalStatusFailed, "admin-1", sqlmock.AnyArg(), "", "funds insufficient", sqlmock.AnyArg(), "req-2").
					WillReturnError(errors.New("db write failed"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock, cleanup := newTestRepo(t)
			defer cleanup()

			tt.setupMock(mock)

			err := repo.UpdateWithdrawalRequest(context.Background(), tt.id, tt.status, tt.approvedBy, tt.txHash, tt.failureReason)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCreateTransaction(t *testing.T) {
	now := time.Now().UTC()
	fromAddr := "GFROM"
	toAddr := "GTO"
	desc := "round payout fee"

	tx := &TreasuryTransaction{
		ID:          "tx-1",
		Type:        "fee",
		Asset:       "USDC",
		Amount:      5000,
		From:        &fromAddr,
		To:          &toAddr,
		TxHash:      "hash-123",
		Description: &desc,
		CreatedAt:   now,
	}

	tests := []struct {
		name      string
		setupMock func(mock sqlmock.Sqlmock)
		wantErr   bool
	}{
		{
			name: "success",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO treasury_transactions").
					WithArgs(tx.ID, tx.Type, tx.Asset, tx.Amount, tx.From, tx.To, tx.TxHash, tx.Description, tx.CreatedAt).
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
			wantErr: false,
		},
		{
			name: "db error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO treasury_transactions").
					WithArgs(tx.ID, tx.Type, tx.Asset, tx.Amount, tx.From, tx.To, tx.TxHash, tx.Description, tx.CreatedAt).
					WillReturnError(errors.New("insert error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock, cleanup := newTestRepo(t)
			defer cleanup()

			tt.setupMock(mock)

			err := repo.CreateTransaction(context.Background(), tx)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestListTransactions(t *testing.T) {
	now := time.Now().UTC()
	fromAddr := "GFROM"
	toAddr := "GTO"
	desc := "test fee"

	tests := []struct {
		name      string
		limit     int
		offset    int
		setupMock func(mock sqlmock.Sqlmock)
		wantCount int
		wantErr   bool
	}{
		{
			name:   "success with results",
			limit:  10,
			offset: 0,
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"id", "type", "asset", "amount", "from_address", "to_address", "tx_hash", "description", "created_at",
				}).AddRow("tx-1", "deposit", "XLM", int64(1000), &fromAddr, &toAddr, "hash-1", &desc, now)
				mock.ExpectQuery("SELECT (.+) FROM treasury_transactions ORDER BY created_at DESC LIMIT \\$1 OFFSET \\$2").
					WithArgs(10, 0).
					WillReturnRows(rows)
			},
			wantCount: 1,
			wantErr:   false,
		},
		{
			name:   "error",
			limit:  5,
			offset: 5,
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT (.+) FROM treasury_transactions ORDER BY created_at DESC LIMIT \\$1 OFFSET \\$2").
					WithArgs(5, 5).
					WillReturnError(errors.New("db failure"))
			},
			wantCount: 0,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock, cleanup := newTestRepo(t)
			defer cleanup()

			tt.setupMock(mock)

			txs, err := repo.ListTransactions(context.Background(), tt.limit, tt.offset)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, txs)
			} else {
				require.NoError(t, err)
				assert.Len(t, txs, tt.wantCount)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCountTransactions(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(mock sqlmock.Sqlmock)
		wantCount int
		wantErr   bool
	}{
		{
			name: "success count",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"count"}).AddRow(42)
				mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM treasury_transactions").
					WillReturnRows(rows)
			},
			wantCount: 42,
			wantErr:   false,
		},
		{
			name: "error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM treasury_transactions").
					WillReturnError(errors.New("count failed"))
			},
			wantCount: 0,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock, cleanup := newTestRepo(t)
			defer cleanup()

			tt.setupMock(mock)

			count, err := repo.CountTransactions(context.Background())
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, 0, count)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, count)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestModelConstants(t *testing.T) {
	assert.Equal(t, WithdrawalStatus("pending"), WithdrawalStatusPending)
	assert.Equal(t, WithdrawalStatus("approved"), WithdrawalStatusApproved)
	assert.Equal(t, WithdrawalStatus("rejected"), WithdrawalStatusRejected)
	assert.Equal(t, WithdrawalStatus("completed"), WithdrawalStatusCompleted)
	assert.Equal(t, WithdrawalStatus("failed"), WithdrawalStatusFailed)

	assert.Equal(t, ApprovalMethod("multisig"), ApprovalMethodMultiSig)
	assert.Equal(t, ApprovalMethod("timelock"), ApprovalMethodTimelock)
}
