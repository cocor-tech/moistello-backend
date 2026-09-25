package stellar_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/stellar"
	"github.com/moistello/backend/pkg/stellar/stellartest"
)

func fastSubmit() stellar.SubmitConfig {
	return stellar.SubmitConfig{MaxAttempts: 3, Backoff: []time.Duration{time.Millisecond, 2 * time.Millisecond}, Timeout: time.Second}
}

func fastPoll() stellar.PollConfig {
	return stellar.PollConfig{Interval: time.Millisecond, Timeout: 500 * time.Millisecond}
}

func TestSubmitter_Submit_SendsJSONRPCAndReturnsHash(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	sub := stellar.NewSubmitter(n.RPCURL(), n.HorizonURL())

	hash, err := sub.SubmitWithRetry(context.Background(), "AAAAsigned", fastSubmit())
	require.NoError(t, err)
	assert.Equal(t, stellartest.TxHash("AAAAsigned"), hash)

	calls := n.RPCCalls("sendTransaction")
	require.Len(t, calls, 1)
	var params struct {
		Transaction string `json:"transaction"`
	}
	require.NoError(t, json.Unmarshal(calls[0], &params))
	assert.Equal(t, "AAAAsigned", params.Transaction)
	assert.Equal(t, "POST", n.Requests()[0].Method)
}

func TestSubmitter_Submit_RetriesRateLimitThenSucceeds(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.QueueRPC("sendTransaction", stellartest.RPCResponse{HTTPStatus: 429, RawBody: "rate limited"})
	sub := stellar.NewSubmitter(n.RPCURL(), n.HorizonURL())

	hash, err := sub.SubmitWithRetry(context.Background(), "env", fastSubmit())
	require.NoError(t, err)
	assert.Equal(t, stellartest.TxHash("env"), hash)
	assert.Len(t, n.RPCCalls("sendTransaction"), 2, "one retry after the 429")
}

func TestSubmitter_Submit_DoesNotRetryBadRequest(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.QueueRPC("sendTransaction", stellartest.RPCResponse{HTTPStatus: 400, RawBody: "malformed envelope"})
	sub := stellar.NewSubmitter(n.RPCURL(), n.HorizonURL())

	_, err := sub.SubmitWithRetry(context.Background(), "env", fastSubmit())
	var txErr *stellar.TransactionError
	require.ErrorAs(t, err, &txErr)
	assert.Equal(t, "TX_BAD_REQUEST", txErr.Code)
	assert.False(t, txErr.IsRetryable)
	assert.Len(t, n.RPCCalls("sendTransaction"), 1)
}

func TestSubmitter_Submit_RetryableRPCErrorExhaustsAttempts(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	for i := 0; i < 3; i++ {
		n.QueueRPC("sendTransaction", stellartest.RPCResponse{Error: &stellartest.RPCError{Code: -32602, Message: "tx_bad_seq: sequence number mismatch"}})
	}
	sub := stellar.NewSubmitter(n.RPCURL(), n.HorizonURL())

	_, err := sub.SubmitWithRetry(context.Background(), "env", fastSubmit())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "submission failed after 3 attempts")
	var txErr *stellar.TransactionError
	require.True(t, errors.As(err, &txErr))
	assert.Equal(t, stellar.ErrCodeBadSequence, txErr.Code)
	assert.Len(t, n.RPCCalls("sendTransaction"), 3)
}

func TestSubmitter_Submit_RejectedStatus(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.QueueRPC("sendTransaction", stellartest.RPCResponse{Result: map[string]any{"hash": "abc", "status": "ERROR"}})
	sub := stellar.NewSubmitter(n.RPCURL(), n.HorizonURL())

	_, err := sub.SubmitWithRetry(context.Background(), "env", fastSubmit())
	require.EqualError(t, err, "transaction rejected: abc")
}

func TestSubmitter_Submit_HonoursContextDuringBackoff(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.QueueRPC("sendTransaction", stellartest.RPCResponse{HTTPStatus: 503, RawBody: "down"})
	sub := stellar.NewSubmitter(n.RPCURL(), n.HorizonURL())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := sub.SubmitWithRetry(ctx, "env", stellar.SubmitConfig{MaxAttempts: 2, Backoff: []time.Duration{time.Minute}})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestSubmitter_PollUntilFinal_WaitsThroughNotFound(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.SetTransactionStatuses("h1", "NOT_FOUND", "NOT_FOUND", "SUCCESS")
	sub := stellar.NewSubmitter(n.RPCURL(), n.HorizonURL())

	res, err := sub.PollUntilFinal(context.Background(), "h1", fastPoll())
	require.NoError(t, err)
	assert.True(t, res.Successful)
	assert.Equal(t, "h1", res.Hash)
	assert.Equal(t, int64(1234), res.Ledger)
	assert.Equal(t, int64(100), res.FeeCharged)
	assert.NotEmpty(t, res.ResultXDR)
	assert.Len(t, n.RPCCalls("getTransaction"), 3)
}

func TestSubmitter_PollUntilFinal_FailedTransaction(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.SetTransactionStatuses("h2", "FAILED")
	sub := stellar.NewSubmitter(n.RPCURL(), n.HorizonURL())

	res, err := sub.PollUntilFinal(context.Background(), "h2", fastPoll())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transaction failed")
	require.NotNil(t, res, "the failed result is still returned for diagnostics")
	assert.False(t, res.Successful)
}

func TestSubmitter_PollUntilFinal_TimesOut(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	sub := stellar.NewSubmitter(n.RPCURL(), n.HorizonURL())

	_, err := sub.PollUntilFinal(context.Background(), "never", stellar.PollConfig{Interval: time.Millisecond, Timeout: 30 * time.Millisecond})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not confirmed after")
}

func TestSubmitter_PollUntilFinal_RPCFailureAborts(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.QueueRPC("getTransaction", stellartest.RPCResponse{HTTPStatus: 200, RawBody: "not json"})
	sub := stellar.NewSubmitter(n.RPCURL(), n.HorizonURL())

	_, err := sub.PollUntilFinal(context.Background(), "h", fastPoll())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding tx status")
}
