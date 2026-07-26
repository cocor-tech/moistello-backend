package stellar

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExecuteWithBackoff_SuccessFirstAttempt(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	err := ExecuteWithBackoff(ctx, "test_op", func(ctx context.Context) error {
		attempts++
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, attempts)
}

func TestExecuteWithBackoff_SuccessAfterRetries(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	cfg := BackoffConfig{
		MaxRetries: 3,
		BaseDelay:  1 * time.Millisecond, // Fast test execution
	}

	err := ExecuteWithBackoffConfig(ctx, "test_op", cfg, func(ctx context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary RPC timeout")
		}
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 3, attempts)
}

func TestExecuteWithBackoff_MaxRetriesExceeded(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	cfg := BackoffConfig{
		MaxRetries: 3,
		BaseDelay:  1 * time.Millisecond,
	}

	err := ExecuteWithBackoffConfig(ctx, "test_op", cfg, func(ctx context.Context) error {
		attempts++
		return errors.New("persistent RPC error")
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed after 3 retries")
	assert.Equal(t, 4, attempts) // Initial attempt + 3 retries
}

func TestExecuteWithBackoff_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := BackoffConfig{
		MaxRetries: 3,
		BaseDelay:  50 * time.Millisecond,
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := ExecuteWithBackoffConfig(ctx, "test_op", cfg, func(ctx context.Context) error {
		return errors.New("RPC timeout")
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}
