// Package push sends push notifications via Firebase Cloud Messaging's
// legacy HTTP API.
//
// Mirrors internal/domain/email/service.go's Brevo client shape (Config,
// Service, WithHTTPClient for testing, graceful no-op when unconfigured,
// exponential-backoff retry on transient failures) — see #191.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/moistello/backend/pkg/logger"
)

// Config holds FCM push notification sending configuration.
type Config struct {
	ServerKey  string
	BaseURL    string
	MaxRetries int
}

// Service sends push notifications via FCM.
type Service struct {
	config Config
	client *http.Client
}

func NewService(cfg Config) *Service {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://fcm.googleapis.com/fcm/send"
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	return &Service{
		config: cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// WithHTTPClient sets a custom http.Client (e.g. for testing).
func (s *Service) WithHTTPClient(client *http.Client) *Service {
	if client != nil {
		s.client = client
	}
	return s
}

// Send delivers a push notification to the device identified by `token`
// (an FCM registration token).
func (s *Service) Send(ctx context.Context, token, title, body string) error {
	log := logger.Ctx(ctx)
	if strings.TrimSpace(s.config.ServerKey) == "" {
		log.Warn().Str("token", token).Msg("fcm server key is not configured — push sending skipped (dev mode)")
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("push: recipient has no registered device token")
	}

	payload := map[string]any{
		"to": token,
		"notification": map[string]string{
			"title": title,
			"body":  body,
		},
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	maxAttempts := s.config.MaxRetries
	var lastErr error
	backoff := 25 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", s.config.BaseURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "key="+s.config.ServerKey)

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("network error sending via fcm (attempt %d/%d): %w", attempt, maxAttempts, err)
			log.Warn().Err(lastErr).Int("attempt", attempt).Msg("fcm delivery failed, retrying...")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
				continue
			}
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode < 300 {
			log.Info().Str("token", token).Msg("push sent via fcm")
			return nil
		}

		// Non-retryable client errors (except 429 Too Many Requests)
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return fmt.Errorf("fcm API error (status %d): %s", resp.StatusCode, string(respBody))
		}

		lastErr = fmt.Errorf("fcm API server error (status %d, attempt %d/%d): %s", resp.StatusCode, attempt, maxAttempts, string(respBody))
		log.Warn().Err(lastErr).Int("attempt", attempt).Int("status", resp.StatusCode).Msg("fcm server error, retrying...")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}

	return fmt.Errorf("all %d attempts to send push via fcm failed: %w", maxAttempts, lastErr)
}
