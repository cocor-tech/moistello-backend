package payout

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/money"
)

// splitPayout computes the gross pool, platform fee and net payout for a
// round using the fixed-point money type. Funds are conserved exactly: the
// fee and the net payout always sum back to the gross pool.
func splitPayout(memberCount int, contribution money.Money, feePercent float64) (gross, fee, net money.Money, err error) {
	if gross, err = contribution.MulInt(int64(memberCount)); err != nil {
		return
	}
	if fee, err = gross.Percent(feePercent); err != nil {
		return
	}
	net, err = gross.Sub(fee)
	return
}

func TestPayout_DifferentialPropertyTest_1000Cases(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	for i := 0; i < 1500; i++ {
		memberCount := rng.Intn(99) + 2                                          // 2 to 100 members
		contribution := money.FromStroops(int64(rng.Intn(100000)+100) * 100_000) // 1.00 to 1000.00
		feePercent := float64(rng.Intn(1000)) / 100.0                            // 0.00% to 10.00%

		gross, fee, net, err := splitPayout(memberCount, contribution, feePercent)
		require.NoError(t, err)

		expectedGross, err := contribution.MulInt(int64(memberCount))
		require.NoError(t, err)
		require.True(t, gross.Equal(expectedGross), "gross mismatch at iteration %d", i)

		sum, err := fee.Add(net)
		require.NoError(t, err)
		require.True(t, sum.Equal(gross), "conservation of funds at iteration %d: %s + %s != %s", i, fee, net, gross)
		require.False(t, fee.IsNegative(), "fee negative at iteration %d", i)
		require.LessOrEqual(t, fee.Stroops(), gross.Stroops(), "fee exceeds gross at iteration %d", i)

		// The fee is the exact percentage rounded to the nearest stroop, so it
		// never drifts more than half a stroop from the real value.
		exact := float64(gross.Stroops()) * feePercent / 100.0
		require.InDelta(t, exact, float64(fee.Stroops()), 0.5, "fee rounding at iteration %d", i)
	}
}
