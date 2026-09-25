package circle

import "github.com/moistello/backend/pkg/money"

// LateFee returns lateFeePercent of the contribution, rounded to the nearest stroop.
func LateFee(contribution money.Money, lateFeePercent float64) (money.Money, error) {
	return contribution.Percent(lateFeePercent)
}

// CalculateLateFee is the float adapter over LateFee. Non-finite inputs yield zero.
func CalculateLateFee(contributionAmount float64, lateFeePercent float64) float64 {
	contribution, err := money.FromFloat64(contributionAmount)
	if err != nil {
		return 0
	}
	fee, err := LateFee(contribution, lateFeePercent)
	if err != nil {
		return 0
	}
	return fee.Float64()
}

// EarlyExitPenalty charges (strikes+1)/10 of the collateral, capped at the
// member's total contributions. A member who has contributed nothing forfeits
// the full collateral.
func EarlyExitPenalty(totalContributed, collateral money.Money, strikes int) (money.Money, error) {
	if !totalContributed.IsPositive() {
		return collateral, nil
	}
	penalty, err := collateral.MulDiv(int64(strikes+1), 10)
	if err != nil {
		return money.Money{}, err
	}
	if penalty.Cmp(totalContributed) > 0 {
		penalty = totalContributed
	}
	return penalty, nil
}

// CalculateEarlyExitPenalty is the float adapter over EarlyExitPenalty.
// Non-finite inputs yield zero.
func CalculateEarlyExitPenalty(totalContributed float64, collateralAmount float64, strikes int) float64 {
	contributed, err := money.FromFloat64(totalContributed)
	if err != nil {
		return 0
	}
	collateral, err := money.FromFloat64(collateralAmount)
	if err != nil {
		return 0
	}
	penalty, err := EarlyExitPenalty(contributed, collateral, strikes)
	if err != nil {
		return 0
	}
	return penalty.Float64()
}

func ShouldRemoveMember(strikes int, maxStrikes int) bool {
	return strikes >= maxStrikes
}

func ApplyStrikes(member *CircleMember, penaltyType string) int {
	add := 1
	if penaltyType == "severe" {
		add = 3
	}
	return add
}
