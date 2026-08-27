// Package sms sends SMS messages via the Twilio REST API.
//
// Mirrors internal/domain/email/service.go's Brevo client shape (Config,
// Service, WithHTTPClient for testing, graceful no-op when unconfigured,
// exponential-backoff retry on transient failures) — see #191, which asks
// for an SMS delivery channel to match the email channel that already
// existed.
package sms

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/moistello/backend/pkg/logger"
)

// Config holds Twilio SMS sending configuration.
type Config struct {
	AccountSID string
	AuthToken  string
	FromNumber string
	BaseURL    string
	MaxRetries int
}

// Service sends SMS messages via the Twilio REST API.
type Service struct {
	config Config
	client *http.Client
}

func NewService(cfg Config) *Service {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.twilio.com/2010-04-01"
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

// Send delivers a single SMS message to `to` (E.164 format).
func (s *Service) Send(ctx context.Context, to, body string) error {
	log := logger.Ctx(ctx)
	if strings.TrimSpace(s.config.AccountSID) == "" || strings.TrimSpace(s.config.AuthToken) == "" {
		log.Warn().Str("to", to).Msg("twilio credentials are not configured — SMS sending skipped (dev mode)")
		return nil
	}
	if strings.TrimSpace(s.config.FromNumber) == "" {
		return fmt.Errorf("sms: FromNumber is not configured")
	}

	endpoint := fmt.Sprintf("%s/Accounts/%s/Messages.json", s.config.BaseURL, s.config.AccountSID)
	form := url.Values{}
	form.Set("To", to)
	form.Set("From", s.config.FromNumber)
	form.Set("Body", body)

	basicAuth := base64.StdEncoding.EncodeToString([]byte(s.config.AccountSID + ":" + s.config.AuthToken))

	maxAttempts := s.config.MaxRetries
	var lastErr error
	backoff := 25 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", "Basic "+basicAuth)

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("network error sending via twilio (attempt %d/%d): %w", attempt, maxAttempts, err)
			log.Warn().Err(lastErr).Int("attempt", attempt).Msg("twilio delivery failed, retrying...")
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
			log.Info().Str("to", to).Msg("sms sent via twilio")
			return nil
		}

		// Non-retryable client errors (except 429 Too Many Requests)
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return fmt.Errorf("twilio API error (status %d): %s", resp.StatusCode, string(respBody))
		}

		lastErr = fmt.Errorf("twilio API server error (status %d, attempt %d/%d): %s", resp.StatusCode, attempt, maxAttempts, string(respBody))
		log.Warn().Err(lastErr).Int("attempt", attempt).Int("status", resp.StatusCode).Msg("twilio server error, retrying...")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}

	return fmt.Errorf("all %d attempts to send sms via twilio failed: %w", maxAttempts, lastErr)
}
