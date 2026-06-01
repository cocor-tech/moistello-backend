package auth

import (
	"context"
	"time"
)

type RateLimiter interface {
	Check(ctx context.Context, key string, maxRequests int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
	Increment(ctx context.Context, key string, window time.Duration) error
	Reset(ctx context.Context, key string) error
}
