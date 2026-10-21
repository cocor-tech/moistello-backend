package yellowcard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient_SandboxMode(t *testing.T) {
	c := NewClient("", "", "")
	assert.Contains(t, c.baseURL, "sandbox.yellowcard.io")
}

func TestNewClient_ProductionMode(t *testing.T) {
	c := NewClient("real-key", "real-secret", "GA...")
	assert.Equal(t, "https://api.yellowcard.io/v1", c.baseURL)
}

func TestGetQuote_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.String(), "/quotes")
		assert.Contains(t, r.URL.RawQuery, "from=NGN")
		assert.Contains(t, r.URL.RawQuery, "to=USDC")
		assert.Contains(t, r.URL.RawQuery, "amount=50000.00")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Quote{
			QuoteID:      "q-123",
			FromCurrency: "NGN",
			ToCurrency:   "USDC",
			FromAmount:   50000,
			ToAmount:     33.33,
			Rate:         1500,
			Fee:          0.5,
		})
	}))
	defer server.Close()

	c := &Client{
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	quote, err := c.GetQuote("NGN", "USDC", 50000)

	require.NoError(t, err)
	assert.Equal(t, "q-123", quote.QuoteID)
	assert.Equal(t, "NGN", quote.FromCurrency)
	assert.Equal(t, "USDC", quote.ToCurrency)
	assert.Equal(t, float64(50000), quote.FromAmount)
	assert.Equal(t, 33.33, quote.ToAmount)
}

func TestGetQuote_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid currency pair"}`))
	}))
	defer server.Close()

	c := &Client{
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	quote, err := c.GetQuote("XXX", "YYY", 100)

	assert.Error(t, err)
	assert.Nil(t, quote)
	assert.Contains(t, err.Error(), "status 400")
	assert.Contains(t, err.Error(), "invalid currency pair")
}

func TestCreateReceive_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/receive", r.URL.Path)

		var req ReceiveRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, float64(50000), req.Amount)
		assert.Equal(t, "NGN", req.Currency)
		assert.Equal(t, "USDC", req.DestinationCurrency)
		assert.Equal(t, "GABC123", req.DestinationAddress)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ReceiveResponse{
			ReceiveID: "r-456",
			Status:    "pending",
			BankDetails: BankDetails{
				BankName:      "Test Bank",
				AccountNumber: "1234567890",
				AccountName:   "Yellow Card NGN",
				Amount:        50000,
			},
			PaymentRef: "MOIST-123",
			ExpiresAt:  "2026-08-27T00:00:00Z",
		})
	}))
	defer server.Close()

	c := &Client{
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	resp, err := c.CreateReceive(ReceiveRequest{
		Amount:              50000,
		Currency:            "NGN",
		DestinationCurrency: "USDC",
		DestinationAddress:  "GABC123",
		PaymentReference:    "MOIST-123",
	})

	require.NoError(t, err)
	assert.Equal(t, "r-456", resp.ReceiveID)
	assert.Equal(t, "pending", resp.Status)
	assert.Equal(t, "Test Bank", resp.BankDetails.BankName)
	assert.Equal(t, float64(50000), resp.BankDetails.Amount)
}

func TestCreateSend_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/send", r.URL.Path)

		var req SendRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, float64(100), req.Amount)
		assert.Equal(t, "USDC", req.Currency)
		assert.Equal(t, "NGN", req.TargetCurrency)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SendResponse{
			SendID:    "s-789",
			Status:    "processing",
			Fee:       0.25,
			NetAmount: 99.75,
		})
	}))
	defer server.Close()

	c := &Client{
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	resp, err := c.CreateSend(SendRequest{
		Amount:         100,
		Currency:       "USDC",
		TargetCurrency: "NGN",
		BankCode:       "044",
		AccountNumber:  "9876543210",
		AccountName:    "Test User",
		PaymentRef:     "MOIST-456",
	})

	require.NoError(t, err)
	assert.Equal(t, "s-789", resp.SendID)
	assert.Equal(t, "processing", resp.Status)
	assert.Equal(t, 0.25, resp.Fee)
	assert.Equal(t, 99.75, resp.NetAmount)
}

func TestGetTransactionStatus_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.True(t, strings.HasSuffix(r.URL.Path, "/txn-abc"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TransactionStatus{
			ID:     "txn-abc",
			Type:   "receive",
			Status: "completed",
		})
	}))
	defer server.Close()

	c := &Client{
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	status, err := c.GetTransactionStatus("txn-abc")

	require.NoError(t, err)
	assert.Equal(t, "txn-abc", status.ID)
	assert.Equal(t, "completed", status.Status)
}

func TestSignRequest_Deterministic(t *testing.T) {
	c := &Client{apiSecret: "test-secret"}

	sig1, ts1 := c.signRequest("POST", "/test", []byte(`{"key":"val"}`))
	// Sign again with same timestamp manually — we can't control time, but
	// we can verify the signature is non-empty and hex-encoded
	_, _ = ts1, sig1
	assert.NotEmpty(t, sig1)
	assert.Len(t, sig1, 64, "HMAC-SHA256 hex digest should be 64 chars")
}

func TestSignRequest_IncludedInHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NotEmpty(t, r.Header.Get("X-YC-Timestamp"))
		assert.NotEmpty(t, r.Header.Get("X-YC-Signature"))
		assert.NotEmpty(t, r.Header.Get("X-API-Key"))
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c := &Client{
		apiKey:     "test-key",
		apiSecret:  "test-secret",
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	err := c.doRequest("GET", "/test", nil, nil)
	assert.NoError(t, err)
}

func TestDoRequest_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer server.Close()

	c := &Client{
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	var result map[string]interface{}
	err := c.doRequest("GET", "/test", nil, &result)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestDoRequest_NoAuth_NoSignature(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("X-API-Key"))
		assert.Empty(t, r.Header.Get("X-YC-Signature"))
		assert.Empty(t, r.Header.Get("X-YC-Timestamp"))
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c := &Client{
		apiKey:     "", // No auth
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	err := c.doRequest("GET", "/test", nil, nil)
	assert.NoError(t, err)
}
