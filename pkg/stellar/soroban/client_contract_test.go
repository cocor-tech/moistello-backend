package soroban_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/stellar"
	"github.com/moistello/backend/pkg/stellar/soroban"
	"github.com/moistello/backend/pkg/stellar/stellartest"
)

func fastClient(n *stellartest.Network) *soroban.Client {
	return soroban.NewClient(n.RPCURL()).
		WithTimings(
			soroban.SubmitConfig{MaxRetries: 2, RetryDelay: time.Millisecond},
			soroban.PollConfig{MaxAttempts: 20, Interval: time.Millisecond},
		).
		WithBackoff(stellar.BackoffConfig{MaxRetries: 2, BaseDelay: time.Millisecond})
}

func TestSorobanClient_SendTransaction_PollsUntilSuccess(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	hash := stellartest.TxHash("signed-env")
	n.SetTransactionStatuses(hash, "NOT_FOUND", "SUCCESS")

	res, err := fastClient(n).SendTransaction(context.Background(), "signed-env")
	require.NoError(t, err)
	assert.Equal(t, hash, res.Hash)
	assert.True(t, res.Successful)
	assert.Equal(t, int64(1234), res.Ledger)
	assert.NotEmpty(t, res.ResultXDR, "resultXdr is read from the camelCase field RPC returns")
	assert.Len(t, n.RPCCalls("sendTransaction"), 1)
	assert.Len(t, n.RPCCalls("getTransaction"), 2)
}

func TestSorobanClient_SendTransaction_FailedIsReportedNotErrored(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	hash := stellartest.TxHash("env")
	n.SetTransactionStatuses(hash, "FAILED")

	res, err := fastClient(n).SendTransaction(context.Background(), "env")
	require.NoError(t, err, "a FAILED transaction is a result, not a transport error")
	assert.False(t, res.Successful)
	assert.Equal(t, hash, res.Hash)
}

func TestSorobanClient_SendTransaction_RetriesRetryableSubmitErrors(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.QueueRPC("sendTransaction", stellartest.RPCResponse{HTTPStatus: 429, RawBody: "slow down"})
	n.SetTransactionStatuses(stellartest.TxHash("env"), "SUCCESS")

	res, err := fastClient(n).SendTransaction(context.Background(), "env")
	require.NoError(t, err)
	assert.True(t, res.Successful)
	assert.Len(t, n.RPCCalls("sendTransaction"), 2)
}

func TestSorobanClient_SendTransaction_NonRetryableSubmitError(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.QueueRPC("sendTransaction", stellartest.RPCResponse{HTTPStatus: 400, RawBody: "bad envelope"})

	_, err := fastClient(n).SendTransaction(context.Background(), "env")
	var txErr *stellar.TransactionError
	require.ErrorAs(t, err, &txErr)
	assert.Equal(t, "TX_BAD_REQUEST", txErr.Code)
	assert.Len(t, n.RPCCalls("sendTransaction"), 1)
	assert.Empty(t, n.RPCCalls("getTransaction"), "nothing to poll after a rejected submit")
}

func TestSorobanClient_SendTransaction_RPCErrorObjectAndMissingHash(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.QueueRPC("sendTransaction",
		stellartest.RPCResponse{Error: &stellartest.RPCError{Code: -32000, Message: "TRY_AGAIN_LATER"}},
	)
	_, err := fastClient(n).SendTransaction(context.Background(), "env")
	require.EqualError(t, err, "rpc error -32000: TRY_AGAIN_LATER")

	n.QueueRPC("sendTransaction", stellartest.RPCResponse{Result: map[string]any{"status": "PENDING"}})
	_, err = fastClient(n).SendTransaction(context.Background(), "env")
	require.EqualError(t, err, "no transaction hash in response")
}

func TestSorobanClient_SendTransaction_GivesUpWhenNeverFinal(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	client := soroban.NewClient(n.RPCURL()).WithTimings(
		soroban.SubmitConfig{MaxRetries: 0, RetryDelay: time.Millisecond},
		soroban.PollConfig{MaxAttempts: 3, Interval: time.Millisecond},
	)

	_, err := client.SendTransaction(context.Background(), "env")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not final after 3 attempts")
	assert.Len(t, n.RPCCalls("getTransaction"), 3)
}

func TestSorobanClient_GetTransaction_RetriesServerErrorsWithBackoff(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.QueueRPC("getTransaction",
		stellartest.RPCResponse{HTTPStatus: 500, RawBody: "oops"},
		stellartest.RPCResponse{HTTPStatus: 500, RawBody: "oops"},
	)
	n.SetTransactionStatuses("h", "SUCCESS")

	res, err := fastClient(n).GetTransaction(context.Background(), "h")
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", res["result"].(map[string]any)["status"])
	assert.Len(t, n.RPCCalls("getTransaction"), 3)

	n.QueueRPC("getTransaction",
		stellartest.RPCResponse{HTTPStatus: 500, RawBody: "oops"},
		stellartest.RPCResponse{HTTPStatus: 500, RawBody: "oops"},
		stellartest.RPCResponse{HTTPStatus: 500, RawBody: "oops"},
	)
	_, err = fastClient(n).GetTransaction(context.Background(), "h")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "soroban_rpcCall failed after 2 retries")
}

func TestSorobanClient_GetAccount_UsesHorizonViaParentClient(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	// The Soroban client is constructed with the RPC URL only, so account
	// lookups go to that URL; pin the current behaviour so a regression
	// (or a fix) is visible.
	_, err := soroban.NewClient(n.RPCURL()).GetAccount(context.Background(), "GABC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "soroban: get account GABC")
}
