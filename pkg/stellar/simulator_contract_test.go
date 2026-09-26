package stellar_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/stellar"
	"github.com/moistello/backend/pkg/stellar/stellartest"
)

func buildTx() *stellar.Transaction {
	return stellar.NewTransactionBuilder(testAccount).
		AddSorobanInvoke("CCONTRACT", "stake", []stellar.SorobanArg{{Type: "u64", Value: "5"}}).
		Build(77)
}

func TestSimulator_Simulate_ParsesResultAndSendsTransaction(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.SetSimulateResult(map[string]any{
		"transaction_data": "BASE64DATA",
		"events":           []string{"ev1"},
		"cost":             map[string]any{"cpu_instructions": 250000, "memory_bytes": 12000},
	})

	res, err := stellar.NewSimulator(n.RPCURL()).SimulateTransaction(context.Background(), buildTx())
	require.NoError(t, err)
	assert.Equal(t, "BASE64DATA", res.TransactionData)
	assert.Equal(t, []string{"ev1"}, res.Events)
	assert.Equal(t, uint64(250000), res.Cost.CPUInstructions)
	assert.Equal(t, uint64(12000), res.Cost.MemoryBytes)
	assert.Nil(t, res.Error)

	calls := n.RPCCalls("simulateTransaction")
	require.Len(t, calls, 1)
	var params struct {
		Transaction stellar.Transaction `json:"transaction"`
	}
	require.NoError(t, json.Unmarshal(calls[0], &params))
	assert.Equal(t, testAccount, params.Transaction.SourceAccount)
	assert.Equal(t, int64(77), params.Transaction.Sequence)
	assert.Equal(t, int64(100), params.Transaction.Fee)
	require.Len(t, params.Transaction.Operations, 1)
}

func TestSimulator_Simulate_ContractErrorIsSurfaced(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.SetSimulateResult(map[string]any{"error": "HostError: Error(Contract, #3)", "events": []string{}, "cost": map[string]any{}})

	res, err := stellar.NewSimulator(n.RPCURL()).SimulateTransaction(context.Background(), buildTx())
	require.NoError(t, err)
	require.NotNil(t, res.Error)
	assert.Contains(t, *res.Error, "Error(Contract, #3)")
}

func TestSimulator_Simulate_RPCErrorAndServerError(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.QueueRPC("simulateTransaction",
		stellartest.RPCResponse{Error: &stellartest.RPCError{Code: -32602, Message: "invalid params"}},
		stellartest.RPCResponse{HTTPStatus: 502, RawBody: "bad gateway"},
	)
	sim := stellar.NewSimulator(n.RPCURL())

	_, err := sim.SimulateTransaction(context.Background(), buildTx())
	require.EqualError(t, err, "rpc error -32602: invalid params")

	_, err = sim.SimulateTransaction(context.Background(), buildTx())
	var txErr *stellar.TransactionError
	require.ErrorAs(t, err, &txErr)
	assert.Equal(t, "TX_SERVER_ERROR", txErr.Code)
	assert.True(t, txErr.IsRetryable)
}

func TestSimulator_ApplyResources_ScalesFeeWithinBounds(t *testing.T) {
	sim := stellar.NewSimulator("http://unused")

	tx := sim.ApplyResources(buildTx(), &stellar.SimulationResult{Cost: stellar.SimulateCost{CPUInstructions: 50000, MemoryBytes: 500}})
	assert.Equal(t, int64(1000), tx.Fee, "fee = base 100 × (50000/10000 + 500/100)")

	tx = sim.ApplyResources(buildTx(), &stellar.SimulationResult{Cost: stellar.SimulateCost{CPUInstructions: 1e9}})
	assert.Equal(t, int64(100_000), tx.Fee, "cost factor is capped at 1000")

	tx = sim.ApplyResources(buildTx(), &stellar.SimulationResult{})
	assert.Equal(t, int64(100), tx.Fee, "never below the minimum fee")
}
