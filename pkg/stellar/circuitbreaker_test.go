package stellar

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func lastFailureTimeFrom(t *testing.T, cb *CircuitBreaker) time.Time {
	t.Helper()
	got, ok := cb.Stats()["lastFailureTime"].(time.Time)
	if !ok {
		t.Fatalf("Stats()[lastFailureTime] is %T, want time.Time", cb.Stats()["lastFailureTime"])
	}
	return got
}

func TestCircuitBreaker_LastFailureTimeZeroBeforeAnyFailure(t *testing.T) {
	cb := NewCircuitBreaker("rpc", DefaultCircuitBreakerConfig())

	_ = cb.Execute(context.Background(), func() error { return nil })

	assert.True(t, lastFailureTimeFrom(t, cb).IsZero(), "successful calls must not record a failure time")
}

func TestCircuitBreaker_RecordsLastFailureTime(t *testing.T) {
	cb := NewCircuitBreaker("rpc", DefaultCircuitBreakerConfig())

	before := time.Now()
	err := cb.Execute(context.Background(), func() error { return errors.New("boom") })
	after := time.Now()

	assert.EqualError(t, err, "boom")
	got := lastFailureTimeFrom(t, cb)
	assert.False(t, got.Before(before), "failure time %v is before the call started at %v", got, before)
	assert.False(t, got.After(after), "failure time %v is after the call ended at %v", got, after)
}

func TestCircuitBreaker_ResetClearsLastFailureTime(t *testing.T) {
	cb := NewCircuitBreaker("rpc", DefaultCircuitBreakerConfig())
	_ = cb.Execute(context.Background(), func() error { return errors.New("boom") })
	assert.False(t, lastFailureTimeFrom(t, cb).IsZero())

	cb.Reset()

	assert.True(t, lastFailureTimeFrom(t, cb).IsZero())
}
