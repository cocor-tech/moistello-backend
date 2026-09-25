package soroban

import (
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
		{"int64 is already base units", int64(42), stellar.SorobanArg{Type: "i128", Value: "42"}},
		{"uint64 stays u64", uint64(7), stellar.SorobanArg{Type: "u64", Value: "7"}},
		{"address", "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H", stellar.SorobanArg{Type: "address", Value: "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"}},
		{"symbol", "stake", stellar.SorobanArg{Type: "symbol", Value: "stake"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, toSorobanArg(tc.in))
		})
	}
}
