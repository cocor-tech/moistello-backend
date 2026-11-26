package soroban

import (
	"encoding/base64"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/pkg/money"
	"github.com/moistello/backend/pkg/stellar"
)

func TestToSorobanArg_AmountsAreBaseUnits(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want stellar.SorobanArg
	}{
		{"money is passed as stroops", money.MustFromString("1.5"), stellar.SorobanArg{Type: "i128", Value: "15000000"}},
		{"float tokens scale to stroops", 1.5, stellar.SorobanArg{Type: "i128", Value: "15000000"}},
		{"float fractions survive", 0.0000001, stellar.SorobanArg{Type: "i128", Value: "1"}},
		{"float error falls back to 0", math.NaN(), stellar.SorobanArg{Type: "i128", Value: "0"}},
		{"int is base units", int(10), stellar.SorobanArg{Type: "i128", Value: "10"}},
		{"int64 is already base units", int64(42), stellar.SorobanArg{Type: "i128", Value: "42"}},
		{"int32 converts to i32", int32(123), stellar.SorobanArg{Type: "i32", Value: "123"}},
		{"uint32 converts to u32", uint32(456), stellar.SorobanArg{Type: "u32", Value: "456"}},
		{"uint64 stays u64", uint64(7), stellar.SorobanArg{Type: "u64", Value: "7"}},
		{"bool converts to bool", true, stellar.SorobanArg{Type: "bool", Value: "true"}},
		{"bytes convert to base64", []byte("hello"), stellar.SorobanArg{Type: "bytes", Value: base64.StdEncoding.EncodeToString([]byte("hello"))}},
		{"address", "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H", stellar.SorobanArg{Type: "address", Value: "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"}},
		{"symbol", "stake", stellar.SorobanArg{Type: "symbol", Value: "stake"}},
		{"struct default symbol", struct{ X int }{X: 1}, stellar.SorobanArg{Type: "symbol", Value: "{1}"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, toSorobanArg(tc.in))
		})
	}
}

func TestContractInvoker_ApplyResourcesAndContractID(t *testing.T) {
	invoker := NewContractInvoker(nil, nil, nil, "CONTRACT_XYZ")
	assert.Equal(t, "CONTRACT_XYZ", invoker.ContractID())

	t.Run("applyResources clamps fee to minimum 100", func(t *testing.T) {
		tx := &stellar.Transaction{Fee: 10}
		simResult := &stellar.SimulationResult{
			Cost: stellar.SimulationCost{
				CPUInstructions: 1,
				MemoryBytes:     1,
			},
		}
		updatedTx := invoker.applyResources(tx, simResult)
		assert.GreaterOrEqual(t, updatedTx.Fee, int64(100))
	})
}
