package circle

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/money"
)

func TestPayoutPool_ExactInStroops(t *testing.T) {
	pool, err := PayoutPool(money.MustFromString("0.1"), 3, money.MustFromString("0.0000001"))
	require.NoError(t, err)
	assert.Equal(t, "0.3000001", pool.String())

	// The float adapter agrees with the exact computation.
	assert.Equal(t, 0.3000001, CalculatePayout(0.1, 3, 1, 0.0000001))
	assert.Equal(t, 0.0, CalculatePayout(math.NaN(), 3, 1, 0))
}

func TestPayoutHelpers(t *testing.T) {
	order := RandomOrder(42, 8)
	assert.Equal(t, order, RandomOrder(42, 8))
	assert.ElementsMatch(t, []int{0, 1, 2, 3, 4, 5, 6, 7}, order)
	assert.Empty(t, RandomOrder(42, 0))

	winner, bid := AuctionWinner(map[string]float64{"alice": 10, "bob": 25, "carol": 15})
	assert.Equal(t, "bob", winner)
	assert.Equal(t, 25.0, bid)
	winner, bid = AuctionWinner(map[string]float64{})
	assert.Empty(t, winner)
	assert.Zero(t, bid)

	assert.Equal(t, "bob", VoteTally(map[string]int{"alice": 2, "bob": 5, "carol": 3}))
	assert.Empty(t, VoteTally(map[string]int{}))
}

func TestLateFee_RoundsToNearestStroop(t *testing.T) {
	fee, err := LateFee(money.MustFromString("100"), 2.5)
	require.NoError(t, err)
	assert.Equal(t, "2.5000000", fee.String())

	fee, err = LateFee(money.MustFromString("0.0000001"), 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), fee.Stroops(), "half a stroop rounds away from zero")

	assert.Equal(t, 2.5, CalculateLateFee(100, 2.5))
	assert.Equal(t, 0.0, CalculateLateFee(math.Inf(1), 2.5))
}

func TestEarlyExitPenalty(t *testing.T) {
	collateral := money.MustFromString("50")

	full, err := EarlyExitPenalty(money.Zero(), collateral, 0)
	require.NoError(t, err)
	assert.True(t, full.Equal(collateral), "no contributions forfeits the collateral")

	tenth, err := EarlyExitPenalty(money.MustFromString("1000"), collateral, 0)
	require.NoError(t, err)
	assert.Equal(t, "5.0000000", tenth.String())

	strikes, err := EarlyExitPenalty(money.MustFromString("1000"), collateral, 2)
	require.NoError(t, err)
	assert.Equal(t, "15.0000000", strikes.String())

	capped, err := EarlyExitPenalty(money.MustFromString("3"), collateral, 9)
	require.NoError(t, err)
	assert.Equal(t, "3.0000000", capped.String(), "penalty never exceeds contributions")

	assert.Equal(t, 15.0, CalculateEarlyExitPenalty(1000, 50, 2))
}
