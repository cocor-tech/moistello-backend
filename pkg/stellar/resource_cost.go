package stellar

const maxResourceCostFactor uint64 = 1000

// ResourceCostFactor converts simulation resource usage into the bounded fee
// multiplier used by transaction builders. The conversion to int64 is made
// only after the uint64 value has been clamped to a known-safe range.
func ResourceCostFactor(cpuInstructions, memoryBytes uint64) int64 {
	cpuFactor := cpuInstructions / 10_000
	memoryFactor := memoryBytes / 100
	if cpuFactor >= maxResourceCostFactor || memoryFactor >= maxResourceCostFactor || cpuFactor > maxResourceCostFactor-memoryFactor {
		return int64(maxResourceCostFactor)
	}

	factor := cpuFactor + memoryFactor
	if factor == 0 {
		factor = 1
	}

	const maxInt64 = uint64(^uint64(0) >> 1)
	if factor > maxInt64 {
		return int64(maxInt64)
	}
	return int64(factor)
}
