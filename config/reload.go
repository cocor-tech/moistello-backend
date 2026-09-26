package config

import (
	"fmt"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// HotConfig is the whitelisted subset of configuration that can change while
// the process is running. Every other key still requires a restart.
type HotConfig struct {
	LogLevel  string
	RateLimit RateLimitConfig
}

// HotReloader holds the live values of the whitelisted keys. A change is only
// applied when every whitelisted key in the file is valid, so an invalid edit
// leaves the previous values intact.
type HotReloader struct {
	mu  sync.RWMutex
	cur HotConfig
}

// NewHotReloader seeds the live values from the loaded configuration.
func NewHotReloader(cfg *Config) *HotReloader {
	return &HotReloader{
		cur: HotConfig{LogLevel: cfg.Logging.Level, RateLimit: cfg.RateLimit},
	}
}

// Get returns the current live values.
func (h *HotReloader) Get() HotConfig {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cur
}

// RateLimit returns the current rate limits; the outage policy and per-resource
// limits keep their startup values.
func (h *HotReloader) RateLimit() RateLimitConfig {
	return h.Get().RateLimit
}

// Apply validates the whitelisted keys in v (logging.level, rate_limit.global,
// rate_limit.authenticated, rate_limit.auth) and swaps them in atomically.
// Keys absent from v keep their current value. On error nothing changes.
func (h *HotReloader) Apply(v *viper.Viper) (HotConfig, error) {
	next := h.Get()

	if v.IsSet("logging.level") {
		level := strings.ToLower(strings.TrimSpace(v.GetString("logging.level")))
		switch level {
		case "debug", "info", "warn", "error":
			next.LogLevel = level
		default:
			return HotConfig{}, fmt.Errorf("config reload: invalid logging.level %q", v.GetString("logging.level"))
		}
	}

	limits := map[string]*int{
		"rate_limit.global":        &next.RateLimit.Global,
		"rate_limit.authenticated": &next.RateLimit.Authenticated,
		"rate_limit.auth":          &next.RateLimit.Auth,
	}
	for key, dst := range limits {
		if !v.IsSet(key) {
			continue
		}
		n := v.GetInt(key)
		if n <= 0 {
			return HotConfig{}, fmt.Errorf("config reload: %s must be a positive integer, got %q", key, v.GetString(key))
		}
		*dst = n
	}

	h.mu.Lock()
	h.cur = next
	h.mu.Unlock()
	return next, nil
}

// Watch observes the config file and applies whitelisted changes without a
// restart. onChange is called after a successful reload and onError when the
// edited file is rejected (the previous values stay active). It does nothing
// when the configuration comes only from the environment.
func (h *HotReloader) Watch(onChange func(HotConfig), onError func(error)) error {
	v := newViper()
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return nil
		}
		return fmt.Errorf("config watch: reading config file: %w", err)
	}
	v.OnConfigChange(func(fsnotify.Event) {
		next, err := h.Apply(v)
		if err != nil {
			onError(err)
			return
		}
		onChange(next)
	})
	v.WatchConfig()
	return nil
}
