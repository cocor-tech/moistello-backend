package notification_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/internal/domain/notification"
)

// ---------------------------------------------------------------------------
// Buffer cadence (#415)
// ---------------------------------------------------------------------------

func TestDigestBuffer_HoldsEventsUntilCadenceElapses(t *testing.T) {
	buf := notification.NewDigestBuffer()
	userID := uuid.New()
	now := time.Now().UTC()

	require.True(t, buf.Enqueue(userID, event(notification.TypeContributionReceived, now), time.Hour))
	assert.Equal(t, 1, buf.Pending(userID))

	// Not yet due at +59m.
	assert.Empty(t, buf.TakeDue(now.Add(59*time.Minute)))
	assert.Equal(t, 1, buf.Pending(userID))

	// Due at +1h, and draining empties the bucket.
	due := buf.TakeDue(now.Add(time.Hour))
	require.Len(t, due, 1)
	assert.Len(t, due[userID], 1)
	assert.Equal(t, 0, buf.Pending(userID))
}

func TestDigestBuffer_CadenceIsPerUser(t *testing.T) {
	buf := notification.NewDigestBuffer()
	now := time.Now().UTC()
	frequent := uuid.New()
	rare := uuid.New()

	require.True(t, buf.Enqueue(frequent, event(notification.TypeContributionReceived, now), 15*time.Minute))
	require.True(t, buf.Enqueue(rare, event(notification.TypeContributionReceived, now), 24*time.Hour))

	// At +1h only the frequent user's cadence has elapsed.
	due := buf.TakeDue(now.Add(time.Hour))
	assert.Contains(t, due, frequent)
	assert.NotContains(t, due, rare)
	assert.Equal(t, 1, buf.Pending(rare))
}

func TestDigestBuffer_DrainIsAtomicAcrossConcurrentFlushes(t *testing.T) {
	// Concurrent flushes must never both claim the same user's batch, or the
	// user would receive two summaries for one window.
	buf := notification.NewDigestBuffer()
	userID := uuid.New()
	now := time.Now().UTC()
	require.True(t, buf.Enqueue(userID, event(notification.TypeContributionReceived, now), time.Minute))

	var mu sync.Mutex
	claimed := 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			due := buf.TakeDue(now.Add(2 * time.Minute))
			mu.Lock()
			claimed += len(due[userID])
			mu.Unlock()
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, claimed, "each batched event must be summarised exactly once")
}

func TestDigestBuffer_OverflowIsRejectedNotDroppedSilently(t *testing.T) {
	// Batching reduces spam; it must never become a silent drop. Once full the
	// buffer refuses so the caller can fall back to immediate delivery.
	buf := notification.NewDigestBuffer()
	userID := uuid.New()
	now := time.Now().UTC()

	for i := 0; i < notification.MaxDigestBatchSize; i++ {
		require.True(t, buf.Enqueue(userID, event(notification.TypeContributionReceived, now), time.Hour),
			"event %d should be accepted", i)
	}
	assert.False(t, buf.Enqueue(userID, event(notification.TypeContributionReceived, now), time.Hour))
	assert.Equal(t, notification.MaxDigestBatchSize, buf.Pending(userID))
}

func TestDigestBuffer_ConcurrentEnqueuesAreNotLost(t *testing.T) {
	buf := notification.NewDigestBuffer()
	userID := uuid.New()
	now := time.Now().UTC()

	const writers, each = 10, 20
	var mu sync.Mutex
	accepted := 0
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				if buf.Enqueue(userID, event(notification.TypeContributionReceived, now), time.Hour) {
					mu.Lock()
					accepted++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	// The batch cap applies here too, so writers*each (200) cannot all be
	// accepted against MaxDigestBatchSize (100). What must hold is that the
	// buffer holds every event it accepted: a lost update under the lock would
	// show up as accepted > Pending.
	assert.Equal(t, notification.MaxDigestBatchSize, accepted, "the cap is reached exactly once")
	assert.Equal(t, accepted, buf.Pending(userID), "no accepted event may be lost to a concurrent update")
}

func TestDigestPreferences_NormalizeClampsUnsupportedCadences(t *testing.T) {
	// A zero/negative/absurd interval must never be stored verbatim: it would
	// either never fire or flush on every event, defeating batching.
	for _, interval := range []time.Duration{0, -time.Hour, time.Minute, 30 * 24 * time.Hour} {
		got := notification.DigestPreferences{Enabled: true, Interval: interval}.Normalize()
		assert.Equal(t, notification.DefaultDigestInterval, got.Interval,
			"interval %s should normalise to the default", interval)
	}

	// A supported interval is preserved untouched.
	kept := notification.DigestPreferences{Enabled: true, Interval: 90 * time.Minute}.Normalize()
	assert.Equal(t, 90*time.Minute, kept.Interval)
}
