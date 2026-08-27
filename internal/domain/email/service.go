package email

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

// Config holds the Brevo email sending configuration.
type Config struct {
	APIKey      string
	FromAddress string
	FromName    string
	BaseURL     string
	MaxRetries  int
}

// Service handles sending transactional emails via Brevo API.
type Service struct {
	config Config
	client *http.Client
}

func NewService(cfg Config) *Service {
	if cfg.FromAddress == "" {
		cfg.FromAddress = "noreply@moistello.com"
	}
	if cfg.FromName == "" {
		cfg.FromName = "Moistello"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.brevo.com/v3/smtp/email"
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

// SendOTP sends a 6-digit verification code to the user's email.
func (s *Service) SendOTP(ctx context.Context, email, code string) error {
	subject := "Your Moistello verification code"
	body := fmt.Sprintf(`<p>Your Moistello verification code is:</p>
<h2 style="font-size:28px;letter-spacing:6px;text-align:center;padding:16px;background:#f5f5f5;border-radius:8px;font-family:monospace">%s</h2>
<p>This code expires in <strong>5 minutes</strong>. If you did not request this code, please ignore this email.</p>`, code)
	return s.sendBrevo(ctx, email, subject, body)
}

// SendBackupCodes sends backup codes to the user's email.
func (s *Service) SendBackupCodes(ctx context.Context, email string, codes []string) error {
	subject := "Your Moistello backup codes"
	body := `<p>Save these backup codes in a secure place. Each code can be used <strong>only once</strong> to access your account if you lose your authenticator device.</p><br>`
	for _, c := range codes {
		body += fmt.Sprintf(`<code style="display:block;font-size:16px;padding:4px 8px;background:#f5f5f5;border-radius:4px;margin:4px 0;font-family:monospace">%s</code>`, c)
	}
	body += `<br><p><strong>Keep these codes safe. They will not be shown again.</strong></p>`
	return s.sendBrevo(ctx, email, subject, body)
}

// SendNotification sends an arbitrary notification (issue #191's email
// delivery channel) as a plain HTML email. Unlike SendOTP/SendBackupCodes/
// SendRecoveryCode, subject and body are caller-supplied rather than
// templated here, since notification content varies by NotificationType.
func (s *Service) SendNotification(ctx context.Context, to, subject, body string) error {
	return s.sendBrevo(ctx, to, subject, body)
}

// SendRecoveryCode sends a one-time recovery code.
func (s *Service) SendRecoveryCode(ctx context.Context, email, code string) error {
	subject := "Your Moistello account recovery code"
	body := fmt.Sprintf(`<p>Your Moistello recovery code is:</p>
<h2 style="font-size:28px;letter-spacing:6px;text-align:center;padding:16px;background:#f5f5f5;border-radius:8px;font-family:monospace">%s</h2>
<p>This code expires in <strong>15 minutes</strong>. If you did not request this code, please secure your account immediately.</p>`, code)
	return s.sendBrevo(ctx, email, subject, body)
}

func (s *Service) sendBrevo(ctx context.Context, to, subject, htmlBody string) error {
	log := logger.Ctx(ctx)
	if strings.TrimSpace(s.config.APIKey) == "" {
		log.Warn().Str("to", to).Str("subject", subject).Msg("brevo API key is not configured — email sending skipped (dev mode)")
		return nil
	}

	payload := map[string]any{
		"sender": map[string]string{
			"name":  s.config.FromName,
			"email": s.config.FromAddress,
		},
		"to": []map[string]string{
			{"email": to},
		},
		"subject":     subject,
		"htmlContent": htmlBody,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	maxAttempts := s.config.MaxRetries
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	var lastErr error
	backoff := 25 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", s.config.BaseURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api-key", s.config.APIKey)
		if reqID, ok := ctx.Value("requestID").(string); ok && reqID != "" {
			req.Header.Set("X-Request-ID", reqID)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("network error sending via brevo (attempt %d/%d): %w", attempt, maxAttempts, err)
			log.Warn().Err(lastErr).Int("attempt", attempt).Msg("brevo delivery failed, retrying...")
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

		if resp.StatusCode < 400 {
			log.Info().Str("to", to).Str("subject", subject).Msg("email sent via brevo")
			return nil
		}

		// Non-retryable client errors (except 429 Too Many Requests)
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return fmt.Errorf("brevo API error (status %d): %s", resp.StatusCode, string(respBody))
		}

		lastErr = fmt.Errorf("brevo API server error (status %d, attempt %d/%d): %s", resp.StatusCode, attempt, maxAttempts, string(respBody))
		log.Warn().Err(lastErr).Int("attempt", attempt).Int("status", resp.StatusCode).Msg("brevo server error, retrying...")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}

	return fmt.Errorf("all %d attempts to send email via brevo failed: %w", maxAttempts, lastErr)
}
