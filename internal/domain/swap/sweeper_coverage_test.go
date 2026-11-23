package swap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSweeper_NonPositiveIntervalUsesDefault(t *testing.T) {
	svc := NewService(newSweepHarness().repo(), nil, newSweepHarness().users(), newSweepHarness().escrow())

	assert.Equal(t, DefaultSweepInterval, NewSweeper(svc, nil, 0).interval)
	assert.Equal(t, DefaultSweepInterval, NewSweeper(svc, nil, -time.Second).interval)
}

func TestSweeper_ContextCancelEndsLoop(t *testing.T) {
	h := newSweepHarness("offer-1")
	svc := NewService(h.repo(), nil, h.users(), h.escrow())
	sweeper := NewSweeper(svc, nil, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	sweeper.Start(ctx)
	cancel()

	select {
	case <-sweeper.doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("sweep loop did not exit after context cancellation")
	}
}

func TestSweeper_StopWithoutStartReturnsAfterOneInterval(t *testing.T) {
	h := newSweepHarness()
	svc := NewService(h.repo(), nil, h.users(), h.escrow())
	sweeper := NewSweeper(svc, nil, 5*time.Millisecond)

	start := time.Now()
	sweeper.Stop()
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestSweepOnce_PropagatesLockError(t *testing.T) {
	mr, rdb := newTestRedis(t)
	h := newSweepHarness("offer-1")
	svc := NewService(h.repo(), nil, h.users(), h.escrow())
	sweeper := NewSweeper(svc, rdb, time.Minute)

	// Make Redis unreachable so the single-flight lock cannot be acquired.
	mr.Close()

	swept, held, err := sweeper.SweepOnce(context.Background())
	require.Error(t, err)
	assert.False(t, held)
	assert.Equal(t, 0, swept)
	assert.Equal(t, int64(0), h.released.Load())
}
