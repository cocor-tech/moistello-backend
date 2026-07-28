package yellowcard

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	baseURL    string
	apiKey     string
	apiSecret  string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey, apiSecret string) (*Client, error) {
	if apiKey == "" || apiSecret == "" {
		return nil, fmt.Errorf("[SECURITY CRITICAL] Missing Yellow Card API credentials in config")
	}

	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// GenerateHMACSignature creates SHA-256 HMAC signature per Yellow Card API spec:
// HMAC_SHA256(Secret, Timestamp + Method + Path + Body)
func (c *Client) GenerateHMACSignature(timestamp, method, path string, body []byte) string {
	message := fmt.Sprintf("%s%s%s%s", timestamp, method, path, string(body))
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

// DoSignedRequest constructs and executes an HTTP request with HMAC signature headers.
func (c *Client) DoSignedRequest(method, path string, payload []byte) (*http.Response, error) {
	url := fmt.Sprintf("%s%s", c.baseURL, path)
	req, err := http.NewRequest(method, url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := c.GenerateHMACSignature(timestamp, method, path, payload)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-YC-Timestamp", timestamp)
	req.Header.Set("X-YC-API-Key", c.apiKey)
	req.Header.Set("X-YC-Signature", signature)

	return c.httpClient.Do(req)
}
