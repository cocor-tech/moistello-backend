package incentives

import (
	"context"
	"math/rand"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// propertySeed and propertyIterations keep the fuzz budget deterministic so
// any violation reproduces identically in CI.
const (
	propertySeed       = 368
	propertyIterations = 5000
)

// TestCalculateContributionMatch_Properties checks invariants of the match
// reward over seeded random configs and amounts, including boundary values.
func TestCalculateContributionMatch_Properties(t *testing.T) {
	rng := rand.New(rand.NewSource(propertySeed))
	ctx := context.Background()
	userID := uuid.New().String()

	for i := 0; i < propertyIterations; i++ {
		percent := rng.Float64() * 100 // 0..100 so a match never exceeds the contribution
		maxMatch := rng.Float64() * 1e6
		if rng.Intn(10) == 0 {
			percent = float64(rng.Intn(2)) * 100 // exercise 0% and 100%
		}
		if rng.Intn(10) == 0 {
			maxMatch = 0
		}

		repo := newMockRepository()
		repo.config = &IncentiveConfig{
			ContributionMatchPercent: percent,
			ContributionMatchMax:     maxMatch,
		}
		svc := NewService(repo)

		a := rng.Float64() * 1e9
		b := a + rng.Float64()*1e9
		if rng.Intn(10) == 0 {
			a = 0
		}

		matchA, err := svc.CalculateContributionMatch(ctx, userID, a)
		require.NoError(t, err)
		matchB, err := svc.CalculateContributionMatch(ctx, userID, b)
		require.NoError(t, err)

		// Non-negative: a non-negative contribution never yields a negative reward.
		require.GreaterOrEqual(t, matchA, 0.0, "seed=%d i=%d percent=%v max=%v amount=%v", propertySeed, i, percent, maxMatch, a)
		require.GreaterOrEqual(t, matchB, 0.0, "seed=%d i=%d", propertySeed, i)

		// Conservation: the match never exceeds the configured cap nor the
		// contributed amount (percent <= 100).
		require.LessOrEqual(t, matchA, maxMatch, "seed=%d i=%d", propertySeed, i)
		require.LessOrEqual(t, matchA, a, "seed=%d i=%d", propertySeed, i)

		// Monotonicity: a larger contribution never earns a smaller match.
		require.LessOrEqual(t, matchA, matchB, "seed=%d i=%d a=%v b=%v", propertySeed, i, a, b)
	}
}
