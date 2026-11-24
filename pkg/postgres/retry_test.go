package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- isRetryableDBError ---

func TestIsRetryableDBError_NilError(t *testing.T) {
	assert.False(t, isRetryableDBError(nil))
}

func TestIsRetryableDBError_RetryablePatterns(t *testing.T) {
	retryable := []string{
		"connection refused",
		"connection reset by peer",
		"too many connections",
		"deadlock detected in tx",
		"serialization failure occurred",
		"could not serialize access",
		"server closed the connection unexpectedly",
		"i/o timeout waiting for data",
	}
	for _, msg := range retryable {
		t.Run(msg, func(t *testing.T) {
			assert.True(t, isRetryableDBError(errors.New(msg)), "expected retryable: %q", msg)
		})
	}
}

func TestIsRetryableDBError_NonRetryableError(t *testing.T) {
	nonRetryable := []string{
		"duplicate key value violates unique constraint",
		"not null constraint violation",
		"foreign key violation",
		"syntax error at or near",
		"column does not exist",
	}
	for _, msg := range nonRetryable {
		t.Run(msg, func(t *testing.T) {
			assert.False(t, isRetryableDBError(errors.New(msg)), "expected non-retryable: %q", msg)
		})
	}
}

// --- contains / searchString (internal helpers) ---

func TestContains_Found(t *testing.T) {
	assert.True(t, contains("connection refused: dial tcp", "connection refused"))
}

func TestContains_NotFound(t *testing.T) {
	assert.False(t, contains("some other error", "connection refused"))
}

func TestContains_SubstrLongerThanString(t *testing.T) {
	assert.False(t, contains("short", "this is longer than short"))
}

func TestContains_ExactMatch(t *testing.T) {
	assert.True(t, contains("connection refused", "connection refused"))
}

// --- WithRetry ---

func TestWithRetry_SucceedsOnFirstAttempt(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, Backoff: []time.Duration{1 * time.Millisecond}}
	calls := 0

	err := WithRetry(context.Background(), cfg, func() error {
		calls++
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestWithRetry_RetriesOnRetryableError(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, Backoff: []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}}
	calls := 0

	err := WithRetry(context.Background(), cfg, func() error {
		calls++
		if calls < 3 {
			return errors.New("deadlock detected")
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, calls)
}

func TestWithRetry_ExhaustsAttemptsAndWrapsLastError(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, Backoff: []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}}
	sentinel := errors.New("too many connections")

	err := WithRetry(context.Background(), cfg, func() error {
		return sentinel
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "operation failed after 3 attempts")
}

func TestWithRetry_StopsImmediatelyOnNonRetryableError(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, Backoff: []time.Duration{1 * time.Millisecond}}
	calls := 0
	permanent := errors.New("duplicate key value violates unique constraint")

	err := WithRetry(context.Background(), cfg, func() error {
		calls++
		return permanent
	})

	require.Error(t, err)
	assert.Equal(t, 1, calls, "non-retryable error must abort after exactly one attempt")
	assert.ErrorIs(t, err, permanent)
}

func TestWithRetry_CancelledContextAbortsBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := RetryConfig{MaxAttempts: 5, Backoff: []time.Duration{100 * time.Millisecond}}

	// Cancel mid-flight so the second backoff sleep is interrupted.
	calls := 0
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := WithRetry(ctx, cfg, func() error {
		calls++
		return errors.New("connection refused")
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Less(t, calls, 5, "context cancel must abort retry loop early")
}

func TestWithRetry_BackoffIndexClampsToLastElement(t *testing.T) {
	// MaxAttempts > len(Backoff)+1: the extra retries must still use the last
	// backoff entry without panicking on out-of-bounds access.
	cfg := RetryConfig{MaxAttempts: 5, Backoff: []time.Duration{1 * time.Millisecond}}
	sentinel := errors.New("serialization failure")

	err := WithRetry(context.Background(), cfg, func() error {
		return sentinel
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

func TestWithRetry_DefaultRetryConfigIsUsable(t *testing.T) {
	// DefaultRetryConfig has real millisecond backoffs; use a immediately
	// succeeding fn so no actual sleeping occurs.
	err := WithRetry(context.Background(), DefaultRetryConfig, func() error { return nil })
	require.NoError(t, err)
}

func TestWithRetry_ZeroMaxAttemptsNeverExecutesFn(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 0}
	calls := 0

	err := WithRetry(context.Background(), cfg, func() error {
		calls++
		return nil
	})

	// loop body is `attempt < 0` which is immediately false → fn never called,
	// returns nil lastErr.
	require.NoError(t, err)
	assert.Equal(t, 0, calls)
}
