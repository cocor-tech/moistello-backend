package indexer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStallWatchdogAlertsOnceUntilProgress(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	alerts := 0
	watchdog := newStallWatchdogWithClock(time.Minute, func(time.Duration) { alerts++ }, func() time.Time { return now })
	now = now.Add(time.Minute)
	assert.True(t, watchdog.Check())
	assert.False(t, watchdog.Check())
	now = now.Add(10 * time.Minute)
	assert.False(t, watchdog.Check())
	assert.Equal(t, 1, alerts)
	watchdog.Processed()
	now = now.Add(time.Minute)
	assert.True(t, watchdog.Check())
	assert.Equal(t, 2, alerts)
}

func TestStallWatchdogUsesSaneDefault(t *testing.T) {
	watchdog := NewStallWatchdog(0, func(time.Duration) {})
	assert.Equal(t, DefaultStallThreshold, watchdog.threshold)
}
