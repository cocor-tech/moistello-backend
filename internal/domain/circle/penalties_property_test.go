package circle

import (
	"math"
	"testing"

	"github.com/moistello/backend/pkg/money"
	"pgregory.net/rapid"
)

// Property: LateFee(contribution, percent) == contribution * percent / 100
// for all finite, non-negative inputs.
func TestLateFee_Property_RoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		contribution := rapid.Float64Range(0, 1_000_000).Draw(t, "contribution")
		percent := rapid.Float64Range(0, 100).Draw(t, "percent")

		fee, err := LateFee(money.MustFromFloat64(contribution), percent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := contribution * percent / 100
		got := fee.Float64()

		// Allow rounding tolerance of ±1 stroop (0.0000001)
		if math.Abs(got-expected) > 0.0000001 {
			t.Errorf("LateFee(%v, %v) = %v, want ≈ %v", contribution, percent, got, expected)
		}
	})
}

// Property: LateFee is always non-negative for non-negative inputs.
func TestLateFee_Property_NonNegative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		contribution := rapid.Float64Range(0, 1_000_000).Draw(t, "contribution")
		percent := rapid.Float64Range(0, 100).Draw(t, "percent")

		fee, err := LateFee(money.MustFromFloat64(contribution), percent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if fee.Float64() < 0 {
			t.Errorf("LateFee(%v, %v) = %v, want >= 0", contribution, percent, fee.Float64())
		}
	})
}

// Property: LateFee(contribution, 0) == 0 for all contributions.
func TestLateFee_Property_ZeroPercent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		contribution := rapid.Float64Range(0, 1_000_000).Draw(t, "contribution")

		fee, err := LateFee(money.MustFromFloat64(contribution), 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if fee.Float64() != 0 {
			t.Errorf("LateFee(%v, 0) = %v, want 0", contribution, fee.Float64())
		}
	})
}

// Property: LateFee(contribution, 100) == contribution for all contributions.
func TestLateFee_Property_100Percent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		contribution := rapid.Float64Range(0, 1_000_000).Draw(t, "contribution")

		fee, err := LateFee(money.MustFromFloat64(contribution), 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := fee.Float64()
		if math.Abs(got-contribution) > 0.0000001 {
			t.Errorf("LateFee(%v, 100) = %v, want ≈ %v", contribution, got, contribution)
		}
	})
}

// Property: LateFee is idempotent — calling it twice with the same inputs
// produces the same result.
func TestLateFee_Property_Idempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		contribution := rapid.Float64Range(0, 1_000_000).Draw(t, "contribution")
		percent := rapid.Float64Range(0, 100).Draw(t, "percent")

		fee1, err := LateFee(money.MustFromFloat64(contribution), percent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		fee2, err := LateFee(money.MustFromFloat64(contribution), percent)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if fee1.Float64() != fee2.Float64() {
			t.Errorf("LateFee not idempotent: %v != %v", fee1.Float64(), fee2.Float64())
		}
	})
}

// Property: CalculateLateFee handles NaN/Inf inputs gracefully (returns 0).
func TestLateFee_Property_NaNInput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		fee := CalculateLateFee(math.NaN(), 2.5)
		if fee != 0 {
			t.Errorf("CalculateLateFee(NaN, 2.5) = %v, want 0", fee)
		}

		fee = CalculateLateFee(100, math.NaN())
		if fee != 0 {
			t.Errorf("CalculateLateFee(100, NaN) = %v, want 0", fee)
		}

		fee = CalculateLateFee(math.Inf(1), 2.5)
		if fee != 0 {
			t.Errorf("CalculateLateFee(+Inf, 2.5) = %v, want 0", fee)
		}
	})
}
