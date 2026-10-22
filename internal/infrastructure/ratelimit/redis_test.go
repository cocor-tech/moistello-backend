package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis, func()) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	return client, mr, func() {
		_ = client.Close()
		mr.Close()
	}
}

func TestRedisRateLimiter_Check(t *testing.T) {
	ctx := context.Background()

	t.Run("allows requests within limit", func(t *testing.T) {
		client, _, cleanup := newTestRedis(t)
		defer cleanup()

		limiter := NewRedisRateLimiter(client)
		key := "test-user-allow"
		maxRequests := 3
		window := 1 * time.Minute

		for i := 1; i <= maxRequests; i++ {
			allowed, ttl, err := limiter.Check(ctx, key, maxRequests, window)
			require.NoError(t, err)
			assert.True(t, allowed, "request %d should be allowed", i)
			assert.True(t, ttl > 0, "ttl should be positive")
		}

		// Request exceeding limit
		allowed, ttl, err := limiter.Check(ctx, key, maxRequests, window)
		require.NoError(t, err)
		assert.False(t, allowed, "request exceeding limit should not be allowed")
		assert.True(t, ttl > 0, "ttl should be positive when rate limited")
	})

	t.Run("sliding window expires old entries", func(t *testing.T) {
		client, mr, cleanup := newTestRedis(t)
		defer cleanup()

		limiter := NewRedisRateLimiter(client)
		key := "test-user-expire"
		maxRequests := 2
		window := 10 * time.Second

		// Consume 2 requests
		allowed, _, err := limiter.Check(ctx, key, maxRequests, window)
		require.NoError(t, err)
		assert.True(t, allowed)

		allowed, _, err = limiter.Check(ctx, key, maxRequests, window)
		require.NoError(t, err)
		assert.True(t, allowed)

		// Third request blocked
		allowed, _, err = limiter.Check(ctx, key, maxRequests, window)
		require.NoError(t, err)
		assert.False(t, allowed)

		// Fast forward miniredis past the window
		mr.FastForward(11 * time.Second)

		// Now should be allowed again
		allowed, _, err = limiter.Check(ctx, key, maxRequests, window)
		require.NoError(t, err)
		assert.True(t, allowed, "request should be allowed after window passes")
	})

	t.Run("returns error when redis is unavailable", func(t *testing.T) {
		unreachableClient := redis.NewClient(&redis.Options{
			Addr:        "localhost:19999",
			DB:          0,
			DialTimeout: 50 * time.Millisecond,
		})
		defer func() { _ = unreachableClient.Close() }()

		limiter := NewRedisRateLimiter(unreachableClient)
		allowed, _, err := limiter.Check(ctx, "err-key", 5, 1*time.Minute)
		assert.Error(t, err)
		assert.False(t, allowed)
	})
}

func TestRedisRateLimiter_Reset(t *testing.T) {
	ctx := context.Background()

	t.Run("resets rate limit for key", func(t *testing.T) {
		client, _, cleanup := newTestRedis(t)
		defer cleanup()

		limiter := NewRedisRateLimiter(client)
		key := "reset-key"
		maxRequests := 1
		window := 1 * time.Minute

		// Use the 1 allowed request
		allowed, _, err := limiter.Check(ctx, key, maxRequests, window)
		require.NoError(t, err)
		assert.True(t, allowed)

		// Next request is rate limited
		allowed, _, err = limiter.Check(ctx, key, maxRequests, window)
		require.NoError(t, err)
		assert.False(t, allowed)

		// Reset
		err = limiter.Reset(ctx, key)
		require.NoError(t, err)

		// Now allowed again
		allowed, _, err = limiter.Check(ctx, key, maxRequests, window)
		require.NoError(t, err)
		assert.True(t, allowed, "request should be allowed after reset")
	})

	t.Run("reset error on closed redis", func(t *testing.T) {
		unreachableClient := redis.NewClient(&redis.Options{
			Addr:        "localhost:19999",
			DB:          0,
			DialTimeout: 50 * time.Millisecond,
		})
		defer func() { _ = unreachableClient.Close() }()

		limiter := NewRedisRateLimiter(unreachableClient)
		err := limiter.Reset(ctx, "key")
		assert.Error(t, err)
	})
}

func TestInMemoryRateLimiter_Check(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		key         string
		maxRequests int
		window      time.Duration
		requests    int
		wantAllowed []bool
	}{
		{
			name:        "all requests within limit allowed",
			key:         "key-1",
			maxRequests: 3,
			window:      1 * time.Minute,
			requests:    3,
			wantAllowed: []bool{true, true, true},
		},
		{
			name:        "exceeding request blocked",
			key:         "key-2",
			maxRequests: 2,
			window:      1 * time.Minute,
			requests:    4,
			wantAllowed: []bool{true, true, false, false},
		},
		{
			name:        "single request limit",
			key:         "key-3",
			maxRequests: 1,
			window:      1 * time.Minute,
			requests:    2,
			wantAllowed: []bool{true, false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewInMemoryRateLimiter()

			for i := 0; i < tt.requests; i++ {
				allowed, remaining, ttl, err := limiter.Check(ctx, tt.key, tt.maxRequests, tt.window)
				require.NoError(t, err)
				assert.Equal(t, tt.wantAllowed[i], allowed, "request %d allowed mismatch", i+1)

				if allowed {
					assert.True(t, remaining >= 0)
					assert.Equal(t, tt.window, ttl)
				} else {
					assert.Equal(t, 0, remaining)
					assert.True(t, ttl >= 0)
				}
			}
		})
	}
}

func TestInMemoryRateLimiter_KeyIsolation(t *testing.T) {
	ctx := context.Background()
	limiter := NewInMemoryRateLimiter()

	maxRequests := 1
	window := 1 * time.Minute

	// Key A
	allowedA1, _, _, err := limiter.Check(ctx, "user-A", maxRequests, window)
	require.NoError(t, err)
	assert.True(t, allowedA1)

	allowedA2, _, _, err := limiter.Check(ctx, "user-A", maxRequests, window)
	require.NoError(t, err)
	assert.False(t, allowedA2)

	// Key B is unaffected
	allowedB1, _, _, err := limiter.Check(ctx, "user-B", maxRequests, window)
	require.NoError(t, err)
	assert.True(t, allowedB1)
}

func TestInMemoryRateLimiter_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	limiter := NewInMemoryRateLimiter()

	key := "concurrent-user"
	maxRequests := 50
	window := 1 * time.Minute
	concurrency := 100

	var wg sync.WaitGroup
	allowedCount := 0
	var mu sync.Mutex

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _, _, err := limiter.Check(ctx, key, maxRequests, window)
			if err == nil && allowed {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, maxRequests, allowedCount, "exactly maxRequests should be allowed under concurrent load")
}

func TestInMemoryRateLimiter_SlidingWindowExpiry(t *testing.T) {
	ctx := context.Background()
	limiter := NewInMemoryRateLimiter()
	key := "window-expire"
	maxRequests := 2
	window := 20 * time.Millisecond

	allowed, _, _, err := limiter.Check(ctx, key, maxRequests, window)
	require.NoError(t, err)
	assert.True(t, allowed)

	allowed, _, _, err = limiter.Check(ctx, key, maxRequests, window)
	require.NoError(t, err)
	assert.True(t, allowed)

	// Exceeded
	allowed, _, _, err = limiter.Check(ctx, key, maxRequests, window)
	require.NoError(t, err)
	assert.False(t, allowed)

	// Sleep past window
	time.Sleep(30 * time.Millisecond)

	// Now allowed
	allowed, _, _, err = limiter.Check(ctx, key, maxRequests, window)
	require.NoError(t, err)
	assert.True(t, allowed, "request should be allowed after short window expires")
}
