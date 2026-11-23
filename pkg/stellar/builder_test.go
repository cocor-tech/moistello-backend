package stellar

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ParseSequence
// ---------------------------------------------------------------------------

func TestParseSequence_ValidInteger(t *testing.T) {
	seq, err := ParseSequence("12345678")
	require.NoError(t, err)
	assert.Equal(t, int64(12345678), seq)
}

func TestParseSequence_Zero(t *testing.T) {
	seq, err := ParseSequence("0")
	require.NoError(t, err)
	assert.Equal(t, int64(0), seq)
}

func TestParseSequence_NegativeValue(t *testing.T) {
	_, err := ParseSequence("not-a-number")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing sequence")
}

func TestParseSequence_EmptyString(t *testing.T) {
	_, err := ParseSequence("")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// TransactionBuilder
// ---------------------------------------------------------------------------

func TestTransactionBuilder_DefaultFeeAndTimeout(t *testing.T) {
	b := NewTransactionBuilder("GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN")
	tx := b.Build(1)
	assert.Equal(t, int64(100), tx.Fee)
	assert.NotNil(t, tx.TimeBounds)
}

func TestTransactionBuilder_SetFee(t *testing.T) {
	b := NewTransactionBuilder("GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN").SetFee(500)
	tx := b.Build(1)
	assert.Equal(t, int64(500), tx.Fee)
}

func TestTransactionBuilder_SetMemo(t *testing.T) {
	b := NewTransactionBuilder("GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN").SetMemo("hello")
	tx := b.Build(1)
	assert.Equal(t, "hello", tx.Memo)
}

func TestTransactionBuilder_SetTimeout(t *testing.T) {
	b := NewTransactionBuilder("GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN").SetTimeout(60 * time.Second)
	tx := b.Build(1)
	// MaxTime must be at least 60 s beyond MinTime.
	assert.GreaterOrEqual(t, tx.TimeBounds.MaxTime-tx.TimeBounds.MinTime, int64(60))
}

func TestTransactionBuilder_AddCreateAccount(t *testing.T) {
	dest := "GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN"
	b := NewTransactionBuilder("GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN").
		AddCreateAccount(dest, "2.0000000")
	tx := b.Build(1)
	require.Len(t, tx.Operations, 1)

	op, ok := tx.Operations[0].(CreateAccountOp)
	require.True(t, ok, "expected CreateAccountOp")
	assert.Equal(t, dest, op.Destination)
	assert.Equal(t, "2.0000000", op.StartingBalance)
}

func TestTransactionBuilder_AddPayment(t *testing.T) {
	dest := "GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN"
	asset := Asset{Code: "USDC", Issuer: "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5"}
	b := NewTransactionBuilder("GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN").
		AddPayment(dest, asset, "10.0000000")
	tx := b.Build(1)
	require.Len(t, tx.Operations, 1)

	op, ok := tx.Operations[0].(PaymentOp)
	require.True(t, ok, "expected PaymentOp")
	assert.Equal(t, dest, op.Destination)
	assert.Equal(t, "10.0000000", op.Amount)
	assert.Equal(t, "USDC", op.Asset.Code)
}

func TestTransactionBuilder_AddSorobanInvoke(t *testing.T) {
	b := NewTransactionBuilder("GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN").
		AddSorobanInvoke("CONTRACT123", "deposit", []SorobanArg{{Type: "i128", Value: "100"}})
	tx := b.Build(1)
	require.Len(t, tx.Operations, 1)

	op, ok := tx.Operations[0].(SorobanInvokeOp)
	require.True(t, ok, "expected SorobanInvokeOp")
	assert.Equal(t, "CONTRACT123", op.ContractID)
	assert.Equal(t, "deposit", op.Function)
	require.Len(t, op.Args, 1)
	assert.Equal(t, "i128", op.Args[0].Type)
}

func TestTransactionBuilder_MultipleOperations(t *testing.T) {
	b := NewTransactionBuilder("GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN").
		AddCreateAccount("GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN", "1").
		AddPayment("GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN", Asset{}, "5")
	tx := b.Build(42)
	assert.Len(t, tx.Operations, 2)
	assert.Equal(t, int64(42), tx.Sequence)
}

func TestTransactionBuilder_SourceAccount(t *testing.T) {
	src := "GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN"
	tx := NewTransactionBuilder(src).Build(1)
	assert.Equal(t, src, tx.SourceAccount)
}

// ---------------------------------------------------------------------------
// Transaction.ToJSON
// ---------------------------------------------------------------------------

func TestTransaction_ToJSON_ValidOutput(t *testing.T) {
	tx := NewTransactionBuilder("GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN").
		SetMemo("test").
		Build(99)

	data, err := tx.ToJSON()
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, float64(99), decoded["sequence"])
	assert.Equal(t, "test", decoded["memo"])
}

func TestTransaction_ToJSON_ContainsSourceAccount(t *testing.T) {
	src := "GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN"
	tx := NewTransactionBuilder(src).Build(1)
	data, err := tx.ToJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), src)
}
