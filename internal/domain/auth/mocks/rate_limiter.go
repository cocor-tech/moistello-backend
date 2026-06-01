package mocks

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"
)

type RateLimiter struct {
	mock.Mock
}

func (m *RateLimiter) Check(ctx context.Context, key string, maxRequests int, window time.Duration) (bool, time.Duration, error) {
	args := m.Called(ctx, key, maxRequests, window)
	return args.Bool(0), args.Get(1).(time.Duration), args.Error(2)
}

func (m *RateLimiter) Increment(ctx context.Context, key string, window time.Duration) error {
	args := m.Called(ctx, key, window)
	return args.Error(0)
}

func (m *RateLimiter) Reset(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}
