package stellar

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/rs/zerolog/log"
)

type BackoffConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
}

func DefaultBackoffConfig() BackoffConfig {
	return BackoffConfig{
		MaxRetries: 3,
		BaseDelay:  1 * time.Second,
	}
}

// ExecuteWithBackoff runs the operation fn with exponential backoff and full jitter.
func ExecuteWithBackoff(ctx context.Context, opName string, fn func(ctx context.Context) error) error {
	return ExecuteWithBackoffConfig(ctx, opName, DefaultBackoffConfig(), fn)
}

// ExecuteWithBackoffConfig executes fn with custom backoff settings.
func ExecuteWithBackoffConfig(ctx context.Context, opName string, cfg BackoffConfig, fn func(ctx context.Context) error) error {
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			// Calculate exponential backoff: BaseDelay * 2^(attempt-1)
			backoff := cfg.BaseDelay * (1 << uint(attempt-1))
			// Apply full jitter: random duration between 0 and backoff
			jitter := time.Duration(rand.Int64n(int64(backoff) + 1))
			
			log.Warn().
				Str("op", opName).
				Int("attempt", attempt).
				Int("max_retries", cfg.MaxRetries).
				Dur("backoff_jitter", jitter).
				Err(lastErr).
				Msg("Stellar RPC operation failed; retrying with exponential backoff")

			select {
			case <-ctx.Done():
				return fmt.Errorf("context canceled during backoff for %s: %w", opName, ctx.Err())
			case <-time.After(jitter):
			}
		}

		err := fn(ctx)
		if err == nil {
			return nil
		}

		lastErr = err
	}

	return fmt.Errorf("%s failed after %d retries: %w", opName, cfg.MaxRetries, lastErr)
}
