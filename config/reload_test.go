package config_test

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/config"
)

func newReloadViper(t *testing.T, yaml string) *viper.Viper {
	t.Helper()
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(yaml)))
	return v
}

func newTestReloader() *config.HotReloader {
	return config.NewHotReloader(&config.Config{
		Logging:   config.LoggingConfig{Level: "info"},
		RateLimit: config.RateLimitConfig{Global: 100, Authenticated: 300, Auth: 10, FailClosed: true},
	})
}

func TestHotReloader_AppliesWhitelistedKeys(t *testing.T) {
	h := newTestReloader()

	_, err := h.Apply(newReloadViper(t, "logging:\n  level: debug\nrate_limit:\n  global: 50\n  auth: 3\n"))
	require.NoError(t, err)

	got := h.Get()
	require.Equal(t, "debug", got.LogLevel)
	require.Equal(t, 50, got.RateLimit.Global)
	require.Equal(t, 300, got.RateLimit.Authenticated, "absent keys keep their value")
	require.Equal(t, 3, got.RateLimit.Auth)
	require.True(t, got.RateLimit.FailClosed, "non-whitelisted rate limit keys are untouched")
}

func TestHotReloader_IgnoresNonWhitelistedKeys(t *testing.T) {
	h := newTestReloader()

	_, err := h.Apply(newReloadViper(t, "rate_limit:\n  fail_closed: false\n  otp_limit: 1\nserver:\n  port: 9\n"))
	require.NoError(t, err)

	require.True(t, h.RateLimit().FailClosed)
	require.Equal(t, 0, h.RateLimit().OTPLimit)
}

func TestHotReloader_InvalidConfigKeepsPreviousValues(t *testing.T) {
	h := newTestReloader()

	for _, yaml := range []string{
		"logging:\n  level: loud\nrate_limit:\n  global: 50\n",
		"logging:\n  level: debug\nrate_limit:\n  global: -1\n",
		"rate_limit:\n  auth: abc\n",
	} {
		_, err := h.Apply(newReloadViper(t, yaml))
		require.Error(t, err)

		got := h.Get()
		require.Equal(t, "info", got.LogLevel)
		require.Equal(t, 100, got.RateLimit.Global)
		require.Equal(t, 10, got.RateLimit.Auth)
	}
}
