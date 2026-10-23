package stellar

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// CircuitState represents the state of the circuit breaker
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // Normal operation
	CircuitOpen                         // Failing, reject fast
	CircuitHalfOpen                     // Testing recovery
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreakerConfig configures the circuit breaker
type CircuitBreakerConfig struct {
	FailureThreshold int           // Consecutive failures to open circuit
	SuccessThreshold int           // Consecutive successes in half-open to close
	OpenTimeout      time.Duration // How long to stay open before half-open
	HalfOpenMaxReqs  int           // Max requests in half-open state
}

func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		OpenTimeout:      30 * time.Second,
		HalfOpenMaxReqs:  3,
	}
}

// CircuitBreaker implements the circuit breaker pattern for Stellar RPC calls
type CircuitBreaker struct {
	mu              sync.Mutex
	config          CircuitBreakerConfig
	state           CircuitState
	failures        int
	successes       int
	halfOpenReqs    int
	lastFailureTime time.Time
	lastStateChange time.Time
	name            string
}

func NewCircuitBreaker(name string, config CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		config: config,
		state:  CircuitClosed,
		name:   name,
	}
}

// Execute runs the given function through the circuit breaker
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func() error) error {
	if err := cb.beforeRequest(); err != nil {
		return err
	}

	err := fn()
	cb.afterRequest(err != nil)
	return err
}

// ExecuteWithResult runs a function that returns a value through the circuit breaker
func (cb *CircuitBreaker) ExecuteWithResult(ctx context.Context, fn func() (interface{}, error)) (interface{}, error) {
	if err := cb.beforeRequest(); err != nil {
		return nil, err
	}

	result, err := fn()
	cb.afterRequest(err != nil)
	return result, err
}

func (cb *CircuitBreaker) beforeRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return nil
	case CircuitOpen:
		if time.Since(cb.lastStateChange) > cb.config.OpenTimeout {
			cb.transitionTo(CircuitHalfOpen)
			return nil
		}
		return fmt.Errorf("circuit breaker [%s] is OPEN (failing fast)", cb.name)
	case CircuitHalfOpen:
		if cb.halfOpenReqs >= cb.config.HalfOpenMaxReqs {
			return fmt.Errorf("circuit breaker [%s] is HALF-OPEN at max requests", cb.name)
		}
		cb.halfOpenReqs++
		return nil
	default:
		return nil
	}
}

func (cb *CircuitBreaker) afterRequest(failed bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if failed {
		cb.lastFailureTime = time.Now()
	}

	switch cb.state {
	case CircuitClosed:
		if failed {
			cb.failures++
			if cb.failures >= cb.config.FailureThreshold {
				cb.transitionTo(CircuitOpen)
			}
		} else {
			cb.failures = 0 // Reset on success
		}
	case CircuitHalfOpen:
		if failed {
			cb.transitionTo(CircuitOpen)
		} else {
			cb.successes++
			if cb.successes >= cb.config.SuccessThreshold {
				cb.transitionTo(CircuitClosed)
			}
		}
	case CircuitOpen:
		// No state change in open — wait for timeout
	}
}

func (cb *CircuitBreaker) transitionTo(state CircuitState) {
	cb.state = state
	cb.lastStateChange = time.Now()
	if state == CircuitHalfOpen {
		cb.halfOpenReqs = 0
		cb.successes = 0
	}
	if state == CircuitClosed {
		cb.failures = 0
	}
}

// State returns the current circuit breaker state
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Stats returns circuit breaker statistics
func (cb *CircuitBreaker) Stats() map[string]interface{} {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return map[string]interface{}{
		"state":           cb.state.String(),
		"failures":        cb.failures,
		"successes":       cb.successes,
		"lastFailureTime": cb.lastFailureTime,
		"lastStateChange": cb.lastStateChange,
		"name":            cb.name,
	}
}

// Reset forces the circuit breaker back to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	cb.state = CircuitClosed
	cb.failures = 0
	cb.successes = 0
	cb.halfOpenReqs = 0
	cb.lastFailureTime = time.Time{}
	cb.mu.Unlock()
}
