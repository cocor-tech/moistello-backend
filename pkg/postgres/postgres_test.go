package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/moistello/backend/config"
)

// ValidatePoolSettings emits log warnings; we only test that it completes
// without panicking for every interesting combination.

func TestValidatePoolSettings_UnboundedPool(t *testing.T) {
	// MaxOpenConns <= 0 → warns about unbounded pool.
	assert.NotPanics(t, func() {
		ValidatePoolSettings(config.DatabaseConfig{MaxOpenConns: 0, MaxIdleConns: 5})
	})
}

func TestValidatePoolSettings_IdleExceedsOpen(t *testing.T) {
	// MaxIdleConns > MaxOpenConns > 0 → warns about clamping.
	assert.NotPanics(t, func() {
		ValidatePoolSettings(config.DatabaseConfig{MaxOpenConns: 5, MaxIdleConns: 10})
	})
}

func TestValidatePoolSettings_HighOpenConns(t *testing.T) {
	// MaxOpenConns >= 50 → warns about exhausting max_connections.
	assert.NotPanics(t, func() {
		ValidatePoolSettings(config.DatabaseConfig{MaxOpenConns: 50, MaxIdleConns: 5})
	})
}

func TestValidatePoolSettings_HealthySettings(t *testing.T) {
	// No warnings; no panics.
	assert.NotPanics(t, func() {
		ValidatePoolSettings(config.DatabaseConfig{
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: 5 * time.Minute,
		})
	})
}

func TestValidatePoolSettings_NegativeOpenConns(t *testing.T) {
	assert.NotPanics(t, func() {
		ValidatePoolSettings(config.DatabaseConfig{MaxOpenConns: -1})
	})
}
