package email

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Config holds the Brevo email sending configuration.
type Config struct {
	APIKey      string
	FromAddress string
	FromName    string
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
	return &Service{
		config: cfg,
		client: &http.Client{},
	}
}

// SendOTP sends a 6-digit verification code to the user's email.
func (s *Service) SendOTP(email, code string) error {
	subject := "Your Moistello verification code"
	body := fmt.Sprintf(`<p>Your Moistello verification code is:</p>
<h2 style="font-size:28px;letter-spacing:6px;text-align:center;padding:16px;background:#f5f5f5;border-radius:8px;font-family:monospace">%s</h2>
<p>This code expires in <strong>5 minutes</strong>. If you did not request this code, please ignore this email.</p>`, code)
	return s.sendBrevo(email, subject, body)
}

// SendBackupCodes sends backup codes to the user's email.
func (s *Service) SendBackupCodes(email string, codes []string) error {
	subject := "Your Moistello backup codes"
	body := `<p>Save these backup codes in a secure place. Each code can be used <strong>only once</strong> to access your account if you lose your authenticator device.</p><br>`
	for _, c := range codes {
		body += fmt.Sprintf(`<code style="display:block;font-size:16px;padding:4px 8px;background:#f5f5f5;border-radius:4px;margin:4px 0;font-family:monospace">%s</code>`, c)
	}
	body += `<br><p><strong>Keep these codes safe. They will not be shown again.</strong></p>`
	return s.sendBrevo(email, subject, body)
}

// SendRecoveryCode sends a one-time recovery code.
func (s *Service) SendRecoveryCode(email, code string) error {
	subject := "Your Moistello account recovery code"
	body := fmt.Sprintf(`<p>Your Moistello recovery code is:</p>
<h2 style="font-size:28px;letter-spacing:6px;text-align:center;padding:16px;background:#f5f5f5;border-radius:8px;font-family:monospace">%s</h2>
<p>This code expires in <strong>15 minutes</strong>. If you did not request this code, please secure your account immediately.</p>`, code)
	return s.sendBrevo(email, subject, body)
}

func (s *Service) sendBrevo(to, subject, htmlBody string) error {
	if strings.TrimSpace(s.config.APIKey) == "" {
		return fmt.Errorf("brevo api key is not configured")
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

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.brevo.com/v3/smtp/email", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", s.config.APIKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending via brevo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("brevo API error: %s", resp.Status)
	}

	fmt.Printf("[BREVO] Email sent to %s — subject: %s\n", to, subject)
	return nil
}
