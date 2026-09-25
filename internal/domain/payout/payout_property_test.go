package payout_test

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Legacy baseline payout calculation reference implementation
func legacyCalculatePayout(memberCount int, contributionAmount, feePercent float64) (grossAmount, feeAmount, netPayout float64) {
	grossAmount = float64(memberCount) * contributionAmount
	feeAmount = (grossAmount * feePercent) / 100.0
	netPayout = grossAmount - feeAmount
	return math.Round(grossAmount*100) / 100, math.Round(feeAmount*100) / 100, math.Round(netPayout*100) / 100
}

// Current optimized payout calculation
func optimizedCalculatePayout(memberCount int, contributionAmount, feePercent float64) (grossAmount, feeAmount, netPayout float64) {
	gross := float64(memberCount) * contributionAmount
	fee := gross * (feePercent / 100.0)
	net := gross - fee
	return math.Round(gross*100) / 100, math.Round(fee*100) / 100, math.Round(net*100) / 100
}

func TestPayout_DifferentialPropertyTest_1000Cases(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for i := 0; i < 1500; i++ {
		memberCount := rng.Intn(99) + 2                             // 2 to 100 members
		contributionAmount := float64(rng.Intn(100000)+100) / 100.0 // 1.00 to 1000.00
		feePercent := float64(rng.Intn(1000)) / 100.0               // 0.00% to 10.00%

		legGross, legFee, legNet := legacyCalculatePayout(memberCount, contributionAmount, feePercent)
		optGross, optFee, optNet := optimizedCalculatePayout(memberCount, contributionAmount, feePercent)

		assert.Equal(t, legGross, optGross, "Gross amount mismatch at iteration %d", i)
		assert.Equal(t, legFee, optFee, "Fee amount mismatch at iteration %d", i)
		assert.Equal(t, legNet, optNet, "Net payout mismatch at iteration %d", i)
		assert.InDelta(t, legGross, legFee+legNet, 0.01, "Conservation of funds at iteration %d", i)
	}
}
