package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type BrevoSender struct {
	apiKey      string
	fromEmail   string
	fromName    string
	httpClient  *http.Client
}

type brevoRequest struct {
	Sender      brevoContact   `json:"sender"`
	To          []brevoContact `json:"to"`
	Subject     string         `json:"subject"`
	TextContent string         `json:"textContent"`
}

type brevoContact struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type brevoResponse struct {
	MessageID string `json:"messageId,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

func NewBrevoSender(apiKey, fromEmail, fromName string) *BrevoSender {
	return &BrevoSender{
		apiKey:    apiKey,
		fromEmail: fromEmail,
		fromName:  fromName,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        5,
				IdleConnTimeout:     30 * time.Second,
				DisableCompression:  false,
			},
		},
	}
}

func (s *BrevoSender) Send(ctx context.Context, to, subject, body string) error {
	payload := brevoRequest{
		Sender: brevoContact{
			Email: s.fromEmail,
			Name:  s.fromName,
		},
		To: []brevoContact{
			{Email: to},
		},
		Subject:     subject,
		TextContent: body,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling brevo request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.brevo.com/v3/smtp/email", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("creating brevo request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending brevo request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp brevoResponse
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errResp); decodeErr == nil && errResp.Message != "" {
			return fmt.Errorf("brevo API error (%d): %s", resp.StatusCode, errResp.Message)
		}
		return fmt.Errorf("brevo API error: status %d", resp.StatusCode)
	}

	return nil
}
