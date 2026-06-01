package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisRateLimiter struct {
	client *redis.Client
}

func NewRedisRateLimiter(client *redis.Client) *RedisRateLimiter {
	return &RedisRateLimiter{client: client}
}

func (l *RedisRateLimiter) Check(ctx context.Context, key string, maxRequests int, window time.Duration) (bool, time.Duration, error) {
	redisKey := fmt.Sprintf("ratelimit:%s", key)
	current, err := l.client.Get(ctx, redisKey).Int()
	if err != nil && err != redis.Nil {
		return false, 0, fmt.Errorf("checking rate limit: %w", err)
	}

	if current >= maxRequests {
		ttl, err := l.client.TTL(ctx, redisKey).Result()
		if err != nil {
			ttl = window
		}
		return false, ttl, nil
	}

	return true, 0, nil
}

func (l *RedisRateLimiter) Increment(ctx context.Context, key string, window time.Duration) error {
	redisKey := fmt.Sprintf("ratelimit:%s", key)
	pipe := l.client.Pipeline()
	pipe.Incr(ctx, redisKey)
	pipe.Expire(ctx, redisKey, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("incrementing rate limit: %w", err)
	}
	return nil
}

func (l *RedisRateLimiter) Reset(ctx context.Context, key string) error {
	redisKey := fmt.Sprintf("ratelimit:%s", key)
	if err := l.client.Del(ctx, redisKey).Err(); err != nil {
		return fmt.Errorf("resetting rate limit: %w", err)
	}
	return nil
}
