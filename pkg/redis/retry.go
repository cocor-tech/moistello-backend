package redis

import (
	"context"
	"crypto/rand"
	"math/big"
	"time"
)

// RetryWithJitter executes fn with exponential backoff and randomized full jitter for transient operations.
func RetryWithJitter(ctx context.Context, maxRetries int, baseDelay, maxDelay time.Duration, fn func() error) error {
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}

		if attempt == maxRetries-1 {
			break
		}

		// Calculate exponential backoff: baseDelay * 2^attempt
		backoff := baseDelay * (1 << attempt)
		if backoff > maxDelay {
			backoff = maxDelay
		}

		// Full jitter: uniformly distributed random duration in [0, backoff]
		n, randErr := rand.Int(rand.Reader, big.NewInt(int64(backoff)))
		var sleepDuration time.Duration
		if randErr == nil && n.Int64() > 0 {
			sleepDuration = time.Duration(n.Int64())
		} else {
			sleepDuration = baseDelay
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleepDuration):
		}
	}
	return err
}
