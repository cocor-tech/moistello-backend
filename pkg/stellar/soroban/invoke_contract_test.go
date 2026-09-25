package soroban_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stellar/go/keypair"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/stellar"
	"github.com/moistello/backend/pkg/stellar/soroban"
	"github.com/moistello/backend/pkg/stellar/stellartest"
)

const contractID = "CA3D5KRYM6CB7OWQ6TWYRR3Z4T7GNZLKERYNZGGA5SOAOPIFY6YQGAXE"

type invokerFixture struct {
	net     *stellartest.Network
	invoker *soroban.ContractInvoker
	signer  *stellar.Signer
	address string
}

func newInvokerFixture(t *testing.T) *invokerFixture {
	t.Helper()
	n := stellartest.New()
	t.Cleanup(n.Close)

	kp, err := keypair.Random()
	require.NoError(t, err)
	signer, err := stellar.NewSigner(kp.Seed())
	require.NoError(t, err)
	n.SetAccount(signer.Address(), 900)

	horizon := stellar.NewClient(n.HorizonURL(), n.RPCURL(), "Test SDF Network ; September 2015")
	client := soroban.NewClient(n.RPCURL()).WithTimings(
		soroban.SubmitConfig{MaxRetries: 1, RetryDelay: time.Millisecond},
		soroban.PollConfig{MaxAttempts: 10, Interval: time.Millisecond},
	)
	invoker := soroban.NewContractInvoker(client, signer, stellar.NewAccountManager(horizon, signer.Address()), contractID)
	return &invokerFixture{net: n, invoker: invoker, signer: signer, address: signer.Address()}
}

// decodeEnvelope unpacks the base64 envelope the invoker submits and returns
// the embedded transaction plus the signature list.
func decodeEnvelope(t *testing.T, params json.RawMessage) (stellar.Transaction, []string) {
	t.Helper()
	var p struct {
		Transaction string `json:"transaction"`
	}
	require.NoError(t, json.Unmarshal(params, &p))
	raw, err := base64.StdEncoding.DecodeString(p.Transaction)
	require.NoError(t, err)
	var env struct {
		Tx         string   `json:"tx"`
		Signatures []string `json:"signatures"`
		Network    string   `json:"network"`
	}
	require.NoError(t, json.Unmarshal(raw, &env))
	var tx stellar.Transaction
	require.NoError(t, json.Unmarshal([]byte(env.Tx), &tx))
	assert.Equal(t, "Test SDF Network ; September 2015", env.Network)
	return tx, env.Signatures
}

func TestContractInvoker_ExecuteContractCall_FullLifecycle(t *testing.T) {
	f := newInvokerFixture(t)
	f.net.SetSimulateResult(map[string]any{
		"transaction_data": "X",
		"events":           []string{},
		"cost":             map[string]any{"cpu_instructions": 30000, "memory_bytes": 200},
	})

	// The hash is derived from the envelope, which we only know after the
	// call; script every poll as SUCCESS by answering the first getTransaction
	// with a scripted reply instead.
	f.net.QueueRPC("getTransaction", stellartest.RPCResponse{Result: map[string]any{"status": "SUCCESS", "resultXdr": "AAAA", "ledger": 55}})

	hash, err := f.invoker.InvokeFunction(context.Background(), "stake", f.address, uint64(25))
	require.NoError(t, err)

	sends := f.net.RPCCalls("sendTransaction")
	require.Len(t, sends, 1)
	assert.Equal(t, stellartest.TxHash(extractEnvelope(t, sends[0])), hash)

	tx, sigs := decodeEnvelope(t, sends[0])
	assert.Equal(t, f.address, tx.SourceAccount)
	assert.Equal(t, int64(900), tx.Sequence, "sequence comes from the Horizon account record")
	assert.Equal(t, int64(500), tx.Fee, "base fee 100 × cost factor (30000/10000 + 200/100)")
	require.Len(t, tx.Operations, 1)
	op := tx.Operations[0].(map[string]any)
	assert.Equal(t, contractID, op["contract_id"])
	assert.Equal(t, "stake", op["function"])
	args := op["args"].([]any)
	require.Len(t, args, 2)
	assert.Equal(t, map[string]any{"type": "address", "value": f.address}, args[0])
	assert.Equal(t, map[string]any{"type": "u64", "value": "25"}, args[1])
	require.Len(t, sigs, 1)
	sig, err := base64.StdEncoding.DecodeString(sigs[0])
	require.NoError(t, err)
	assert.Len(t, sig, 64, "ed25519 signature")

	// Order of network calls: Horizon account → simulate → send → poll.
	reqs := f.net.Requests()
	require.GreaterOrEqual(t, len(reqs), 4)
	assert.Equal(t, "/accounts/"+f.address, reqs[0].Path)
	assert.Equal(t, "simulateTransaction", reqs[1].RPCMethod)
	assert.Equal(t, "sendTransaction", reqs[2].RPCMethod)
	assert.Equal(t, "getTransaction", reqs[3].RPCMethod)
}

func TestContractInvoker_SimulationContractErrorStopsBeforeSubmit(t *testing.T) {
	f := newInvokerFixture(t)
	f.net.SetSimulateResult(map[string]any{"error": "HostError: Error(Contract, #7)", "events": []string{}, "cost": map[string]any{}})

	_, err := f.invoker.ExecuteContractCall(context.Background(), "stake", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contract error: HostError: Error(Contract, #7)")
	assert.Empty(t, f.net.RPCCalls("sendTransaction"), "nothing is submitted when simulation fails")
}

func TestContractInvoker_SimulationTransportFailure(t *testing.T) {
	f := newInvokerFixture(t)
	f.net.QueueRPC("simulateTransaction", stellartest.RPCResponse{HTTPStatus: 500, RawBody: "rpc down"})

	_, err := f.invoker.ExecuteContractCall(context.Background(), "stake", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulation failed")
	assert.Empty(t, f.net.RPCCalls("sendTransaction"))
}

func TestContractInvoker_SequenceFetchFailure(t *testing.T) {
	f := newInvokerFixture(t)
	f.net.QueueHorizon(stellartest.HorizonResponse{HTTPStatus: 500, Body: "horizon down"})

	_, err := f.invoker.ExecuteContractCall(context.Background(), "stake", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting sequence")
	assert.Empty(t, f.net.RPCCalls("simulateTransaction"), "no simulation without a sequence number")
}

func TestContractInvoker_FailedOnChainReturnsHashAndError(t *testing.T) {
	f := newInvokerFixture(t)
	f.net.QueueRPC("getTransaction", stellartest.RPCResponse{Result: map[string]any{"status": "FAILED", "resultXdr": "AAAAFAILED", "ledger": 56}})

	hash, err := f.invoker.ExecuteContractCall(context.Background(), "stake", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transaction failed: AAAAFAILED")
	assert.NotEmpty(t, hash, "the hash is returned so callers can record the failed attempt")
}

func TestContractInvoker_SubmitRejectedPropagatesClassifiedError(t *testing.T) {
	f := newInvokerFixture(t)
	f.net.QueueRPC("sendTransaction", stellartest.RPCResponse{HTTPStatus: 400, RawBody: "tx_insufficient_balance"})

	hash, err := f.invoker.ExecuteContractCall(context.Background(), "stake", nil)
	var txErr *stellar.TransactionError
	require.ErrorAs(t, err, &txErr)
	assert.Equal(t, "TX_BAD_REQUEST", txErr.Code)
	assert.Empty(t, hash)
}

func extractEnvelope(t *testing.T, params json.RawMessage) string {
	t.Helper()
	var p struct {
		Transaction string `json:"transaction"`
	}
	require.NoError(t, json.Unmarshal(params, &p))
	return p.Transaction
}
