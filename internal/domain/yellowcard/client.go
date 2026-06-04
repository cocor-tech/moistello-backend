package yellowcard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client communicates with the Yellow Card API for NGN↔USDC conversions.
// The client must be initialized with API credentials obtained from
// https://yellowcard.io after completing the KYB process.
type Client struct {
	apiKey     string
	apiSecret  string
	baseURL    string
	httpClient *http.Client
}

// Quote represents an FX rate quote from Yellow Card.
type Quote struct {
	QuoteID       string  `json:"quoteId"`
	FromCurrency  string  `json:"fromCurrency"`
	ToCurrency    string  `json:"toCurrency"`
	FromAmount    float64 `json:"fromAmount"`
	ToAmount      float64 `json:"toAmount"`
	Rate          float64 `json:"rate"`
	Fee           float64 `json:"fee"`
	FeePercentage float64 `json:"feePercentage"`
	ExpiresAt     string  `json:"expiresAt"`
}

// ReceiveRequest creates a request for Yellow Card to receive NGN and send USDC.
type ReceiveRequest struct {
	Amount              float64 `json:"amount"`
	Currency            string  `json:"currency"`       // "NGN"
	DestinationCurrency string  `json:"destinationCurrency"` // "USDC"
	DestinationAddress  string  `json:"destinationAddress"` // User's Stellar public key
	PaymentReference    string  `json:"paymentReference"`
	CustomerEmail       string  `json:"customerEmail,omitempty"`
}

// ReceiveResponse is the response from creating a receive request.
type ReceiveResponse struct {
	ReceiveID    string `json:"receiveId"`
	Status       string `json:"status"`
	BankDetails  BankDetails `json:"bankDetails"`
	PaymentRef   string `json:"paymentReference"`
	ExpiresAt    string `json:"expiresAt"`
}

// BankDetails contains the bank information the user must send NGN to.
type BankDetails struct {
	BankName      string `json:"bankName"`
	AccountNumber string `json:"accountNumber"`
	AccountName   string `json:"accountName"`
	Amount        float64 `json:"amount"`
}

// SendRequest creates a request to send NGN from USDC.
type SendRequest struct {
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`       // "USDC"
	TargetCurrency string `json:"targetCurrency"` // "NGN"
	BankCode      string  `json:"bankCode"`
	AccountNumber string  `json:"accountNumber"`
	AccountName   string  `json:"accountName"`
	PaymentRef    string  `json:"paymentReference"`
}

// SendResponse is the response from creating a send request.
type SendResponse struct {
	SendID   string `json:"sendId"`
	Status   string `json:"status"`
	Fee      float64 `json:"fee"`
	NetAmount float64 `json:"netAmount"`
}

// TransactionStatus represents the current state of a Yellow Card transaction.
type TransactionStatus struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // "receive" | "send"
	Status    string `json:"status"` // "pending" | "processing" | "completed" | "failed"
	FromAmount float64 `json:"fromAmount"`
	ToAmount   float64 `json:"toAmount"`
	CreatedAt string `json:"createdAt"`
	CompletedAt string `json:"completedAt,omitempty"`
}

// NewClient creates a Yellow Card API client.
// The apiKey and apiSecret can be left empty to use the sandbox environment.
// Set them once you have completed KYB with Yellow Card.
func NewClient(apiKey, apiSecret string) *Client {
	baseURL := "https://api.yellowcard.io/v1"
	if apiKey == "" {
		baseURL = "https://sandbox.yellowcard.io/v1"
	}
	return &Client{
		apiKey:    apiKey,
		apiSecret: apiSecret,
		baseURL:   baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) doRequest(method, path string, body, result interface{}) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	// TODO: Add HMAC signature using c.apiSecret for production
	// See Yellow Card API docs for signature format

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("yellow card API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
	}
	return nil
}

// GetQuote retrieves an FX quote for converting NGN to USDC or vice versa.
func (c *Client) GetQuote(fromCurrency, toCurrency string, amount float64) (*Quote, error) {
	var result Quote
	err := c.doRequest("GET", fmt.Sprintf("/quotes?from=%s&to=%s&amount=%.2f", fromCurrency, toCurrency, amount), nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateReceive initiates a receive (NGN deposit → USDC to user's wallet).
func (c *Client) CreateReceive(req ReceiveRequest) (*ReceiveResponse, error) {
	var result ReceiveResponse
	err := c.doRequest("POST", "/receive", req, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateSend initiates a send (USDC from Yellow Card → NGN to user's bank).
func (c *Client) CreateSend(req SendRequest) (*SendResponse, error) {
	var result SendResponse
	err := c.doRequest("POST", "/send", req, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// GetTransactionStatus checks the status of a deposit or withdrawal.
func (c *Client) GetTransactionStatus(txnID string) (*TransactionStatus, error) {
	var result TransactionStatus
	err := c.doRequest("GET", fmt.Sprintf("/transactions/%s", txnID), nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
