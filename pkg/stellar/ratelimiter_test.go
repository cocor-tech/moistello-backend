package stellar

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// RateLimiter.Wait
// ---------------------------------------------------------------------------

func TestRateLimiter_BurstAllowsImmediateAccess(t *testing.T) {
	rl := NewRateLimiter(10*time.Second, 3) // large delay, 3 burst tokens

	start := time.Now()
	for i := 0; i < 3; i++ {
		require.NoError(t, rl.Wait(context.Background()))
	}
	// All three burst tokens consumed immediately – must not block for 10 s.
	assert.Less(t, time.Since(start), time.Second, "burst tokens must not cause delay")
}

func TestRateLimiter_NoBurstRespectMinDelay(t *testing.T) {
	minDelay := 20 * time.Millisecond
	rl := NewRateLimiter(minDelay, 0) // no burst tokens

	require.NoError(t, rl.Wait(context.Background())) // first call sets lastTx

	start := time.Now()
	require.NoError(t, rl.Wait(context.Background())) // second must wait
	elapsed := time.Since(start)

	// Should wait at least minDelay - small clock tolerance.
	assert.GreaterOrEqual(t, elapsed, minDelay/2,
		"second Wait must respect the minimum delay")
}

func TestRateLimiter_ContextCancelledDuringWait(t *testing.T) {
	rl := NewRateLimiter(500*time.Millisecond, 0)
	require.NoError(t, rl.Wait(context.Background())) // consume initial tick

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()

	err := rl.Wait(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRateLimiter_Reset_RechargesBurstAndClearsLastTx(t *testing.T) {
	rl := NewRateLimiter(10*time.Second, 2)

	// Drain burst tokens.
	require.NoError(t, rl.Wait(context.Background()))
	require.NoError(t, rl.Wait(context.Background()))

	rl.Reset()

	// After reset, burst tokens are recharged and next Wait must not block.
	start := time.Now()
	require.NoError(t, rl.Wait(context.Background()))
	assert.Less(t, time.Since(start), time.Second, "post-reset Wait must be instant")
}

// ---------------------------------------------------------------------------
// ControlledSubmit
// ---------------------------------------------------------------------------

func TestControlledSubmit_SuccessPath(t *testing.T) {
	rl := NewRateLimiter(0, 1)

	hash, err := ControlledSubmit(context.Background(), rl, func() (string, error) {
		return "tx-hash-abc", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "tx-hash-abc", hash)
}

func TestControlledSubmit_SubmitFnErrorPropagates(t *testing.T) {
	rl := NewRateLimiter(0, 1)
	submitErr := errors.New("horizon rejected tx")

	hash, err := ControlledSubmit(context.Background(), rl, func() (string, error) {
		return "", submitErr
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, submitErr)
	assert.Empty(t, hash)
}

func TestControlledSubmit_RateLimiterContextCancelledBeforeSubmit(t *testing.T) {
	rl := NewRateLimiter(500*time.Millisecond, 0)
	require.NoError(t, rl.Wait(context.Background())) // prime lastTx

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, err := ControlledSubmit(ctx, rl, func() (string, error) {
		return "should-not-be-called", nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
