package stellar

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ResourceCostFactor constants (from resource_cost.go).
const maxCostFactor = int64(maxResourceCostFactor)

func TestResourceCostFactor_ZeroInputsReturnsOne(t *testing.T) {
	// cpuFactor=0, memoryFactor=0 → result clamped to minimum 1
	assert.Equal(t, int64(1), ResourceCostFactor(0, 0))
}

func TestResourceCostFactor_TypicalValues(t *testing.T) {
	// cpuInstructions=50_000 → cpuFactor=5
	// memoryBytes=200       → memoryFactor=2
	// total=7
	assert.Equal(t, int64(7), ResourceCostFactor(50_000, 200))
}

func TestResourceCostFactor_CpuAloneExceedsCap(t *testing.T) {
	// cpuInstructions large enough that cpuFactor >= maxResourceCostFactor (1000)
	assert.Equal(t, maxCostFactor, ResourceCostFactor(10_000_000, 0))
}

func TestResourceCostFactor_MemoryAloneExceedsCap(t *testing.T) {
	// memoryBytes = 100*1000 = 100_000 → memoryFactor=1000 >= cap
	assert.Equal(t, maxCostFactor, ResourceCostFactor(0, 100_000))
}

func TestResourceCostFactor_SumExceedsCap(t *testing.T) {
	// cpuFactor=600 (cpu=6_000_000), memoryFactor=600 (mem=60_000)
	// 600+600=1200 > 1000 → cpuFactor > maxResourceCostFactor-memoryFactor
	assert.Equal(t, maxCostFactor, ResourceCostFactor(6_000_000, 60_000))
}

func TestResourceCostFactor_ExactlyAtCpuCap(t *testing.T) {
	// cpuInstructions = maxResourceCostFactor * 10_000 → cpuFactor == cap
	cpu := uint64(maxResourceCostFactor) * 10_000
	assert.Equal(t, maxCostFactor, ResourceCostFactor(cpu, 0))
}

func TestResourceCostFactor_ExactlyAtMemoryCap(t *testing.T) {
	// memoryBytes = maxResourceCostFactor * 100 → memoryFactor == cap
	mem := uint64(maxResourceCostFactor) * 100
	assert.Equal(t, maxCostFactor, ResourceCostFactor(0, mem))
}

func TestResourceCostFactor_SmallNonZeroValues(t *testing.T) {
	// cpuInstructions=10_000 → cpuFactor=1; memoryBytes=100 → memoryFactor=1; total=2
	assert.Equal(t, int64(2), ResourceCostFactor(10_000, 100))
}

func TestResourceCostFactor_IsAlwaysPositive(t *testing.T) {
	cases := [][2]uint64{
		{0, 0},
		{1, 0},
		{0, 1},
		{9_999, 99},
		{10_000, 100},
	}
	for _, c := range cases {
		result := ResourceCostFactor(c[0], c[1])
		assert.Greater(t, result, int64(0), "cpu=%d mem=%d", c[0], c[1])
	}
}
