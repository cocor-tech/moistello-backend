package stellar

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TransactionError
// ---------------------------------------------------------------------------

func TestTransactionError_ErrorString(t *testing.T) {
	err := &TransactionError{Code: "TX_BAD_REQUEST", Message: "bad input"}
	assert.Equal(t, "transaction error [TX_BAD_REQUEST]: bad input", err.Error())
}

// ---------------------------------------------------------------------------
// IsRetryable
// ---------------------------------------------------------------------------

func TestIsRetryable_TransactionErrorRetryable(t *testing.T) {
	err := &TransactionError{Code: ErrCodeRateLimited, IsRetryable: true}
	assert.True(t, IsRetryable(err))
}

func TestIsRetryable_TransactionErrorNonRetryable(t *testing.T) {
	err := &TransactionError{Code: ErrCodeInsufficientBalance, IsRetryable: false}
	assert.False(t, IsRetryable(err))
}

func TestIsRetryable_NonTransactionError(t *testing.T) {
	// A plain error is never retryable by this function.
	assert.False(t, IsRetryable(assert.AnError))
}

// ---------------------------------------------------------------------------
// ClassifyError – HTTP status code branches
// ---------------------------------------------------------------------------

func TestClassifyError_400_NotRetryable(t *testing.T) {
	err := ClassifyError(400, []byte("bad payload"))
	require.Error(t, err)

	txErr, ok := err.(*TransactionError)
	require.True(t, ok)
	assert.Equal(t, "TX_BAD_REQUEST", txErr.Code)
	assert.False(t, txErr.IsRetryable)
	assert.Equal(t, "bad payload", txErr.Message)
}

func TestClassifyError_429_Retryable(t *testing.T) {
	err := ClassifyError(429, []byte("too many requests"))
	txErr := err.(*TransactionError)
	assert.Equal(t, "TX_RATE_LIMITED", txErr.Code)
	assert.True(t, txErr.IsRetryable)
}

func TestClassifyError_500_Retryable(t *testing.T) {
	err := ClassifyError(500, []byte("internal error"))
	txErr := err.(*TransactionError)
	assert.Equal(t, "TX_SERVER_ERROR", txErr.Code)
	assert.True(t, txErr.IsRetryable)
}

func TestClassifyError_503_Retryable(t *testing.T) {
	err := ClassifyError(503, []byte("service unavailable"))
	txErr := err.(*TransactionError)
	assert.Equal(t, "TX_SERVER_ERROR", txErr.Code)
	assert.True(t, txErr.IsRetryable)
}

func TestClassifyError_InsufficientBalance_NotRetryable(t *testing.T) {
	err := ClassifyError(200, []byte("insufficient_balance in account"))
	txErr := err.(*TransactionError)
	assert.Equal(t, ErrCodeInsufficientBalance, txErr.Code)
	assert.False(t, txErr.IsRetryable)
}

func TestClassifyError_Expired_Retryable(t *testing.T) {
	err := ClassifyError(200, []byte("ledger entry expired"))
	txErr := err.(*TransactionError)
	assert.Equal(t, ErrCodeExpired, txErr.Code)
	assert.True(t, txErr.IsRetryable)
}

func TestClassifyError_BadSequence_Retryable(t *testing.T) {
	err := ClassifyError(200, []byte("bad sequence number"))
	txErr := err.(*TransactionError)
	assert.Equal(t, ErrCodeBadSequence, txErr.Code)
	assert.True(t, txErr.IsRetryable)
}

func TestClassifyError_InsufficientFee_Retryable(t *testing.T) {
	err := ClassifyError(200, []byte("fee too low"))
	txErr := err.(*TransactionError)
	assert.Equal(t, "TX_INSUFFICIENT_FEE", txErr.Code)
	assert.True(t, txErr.IsRetryable)
}

func TestClassifyError_Unknown_NotRetryable(t *testing.T) {
	err := ClassifyError(200, []byte("completely unrecognised error"))
	txErr := err.(*TransactionError)
	assert.Equal(t, "TX_UNKNOWN", txErr.Code)
	assert.False(t, txErr.IsRetryable)
}

func TestClassifyError_EmptyBody(t *testing.T) {
	err := ClassifyError(400, []byte{})
	txErr := err.(*TransactionError)
	assert.Equal(t, "TX_BAD_REQUEST", txErr.Code)
	assert.Equal(t, "", txErr.Message)
}
