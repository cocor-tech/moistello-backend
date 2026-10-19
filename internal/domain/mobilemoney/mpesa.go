package mobilemoney

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// MPesaConfig holds the Safaricom Daraja API credentials for one M-Pesa
// paybill/till. Obtained from https://developer.safaricom.co.ke after
// production go-live approval; sandbox credentials work unchanged against
// the sandbox base URL.
type MPesaConfig struct {
	ConsumerKey    string
	ConsumerSecret string
	Shortcode      string // paybill or till number (BusinessShortCode)
	Passkey        string // Lipa na M-Pesa online passkey, for STK push password
	// SecurityCredential is the initiator password encrypted with Safaricom's
	// public certificate, generated once via Daraja's certificate and
	// provided here rather than computed per-request.
	SecurityCredential string
	InitiatorName      string
	CallbackBaseURL    string // public HTTPS base URL for STK/B2C result callbacks
	Sandbox            bool
}

// MPesaProvider implements Provider against Safaricom's Daraja API for
// KES (M-Pesa). InitiateOnramp uses Lipa na M-Pesa Online (STK Push) to
// collect from the customer; InitiateOfframp uses B2C to disburse.
type MPesaProvider struct {
	cfg        MPesaConfig
	baseURL    string
	httpClient *http.Client

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

func NewMPesaProvider(cfg MPesaConfig) *MPesaProvider {
	baseURL := "https://api.safaricom.co.ke"
	if cfg.Sandbox {
		baseURL = "https://sandbox.safaricom.co.ke"
	}
	return &MPesaProvider{
		cfg:        cfg,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// SetHTTPClient overrides the underlying HTTP client. Intended for tests.
func (p *MPesaProvider) SetHTTPClient(client *http.Client) { p.httpClient = client }

// SetBaseURL overrides the API base URL. Intended for tests.
func (p *MPesaProvider) SetBaseURL(baseURL string) { p.baseURL = baseURL }

func (p *MPesaProvider) Name() string     { return "mpesa" }
func (p *MPesaProvider) Currency() string { return "KES" }

// accessToken returns a cached OAuth token, fetching a new one if the
// current one is missing or about to expire.
func (p *MPesaProvider) accessToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.token != "" && time.Now().Before(p.tokenExpiry) {
		return p.token, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/oauth/v1/generate?grant_type=client_credentials", nil)
	if err != nil {
		return "", fmt.Errorf("building oauth request: %w", err)
	}
	req.SetBasicAuth(p.cfg.ConsumerKey, p.cfg.ConsumerSecret)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting oauth token: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn().Err(err).Msg("failed to close M-Pesa response body")
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading oauth response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("mpesa oauth error (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   string `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing oauth response: %w", err)
	}

	expiresIn, err := strconv.Atoi(result.ExpiresIn)
	if err != nil || expiresIn <= 0 {
		expiresIn = 3600
	}

	p.token = result.AccessToken
	// Refresh a little early so an in-flight request never races expiry.
	p.tokenExpiry = time.Now().Add(time.Duration(expiresIn)*time.Second - 60*time.Second)
	return p.token, nil
}

func (p *MPesaProvider) doRequest(ctx context.Context, path string, body, result any) error {
	token, err := p.accessToken(ctx)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn().Err(err).Msg("failed to close M-Pesa response body")
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("mpesa API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
	}
	return nil
}

// stkPassword builds the Lipa na M-Pesa Online password: base64(Shortcode +
// Passkey + Timestamp), as required by the STK Push API.
func stkPassword(shortcode, passkey, timestamp string) string {
	return base64.StdEncoding.EncodeToString([]byte(shortcode + passkey + timestamp))
}

// Quote returns a 1:1 KES/USDC rate placeholder. M-Pesa itself has no FX
// endpoint — real deployments should source the rate from a treasury/oracle
// service and pass it in; this keeps the Provider interface satisfiable
// without pretending Daraja provides FX quotes it doesn't.
func (p *MPesaProvider) Quote(_ context.Context, toCurrency string, usdcAmount float64) (*Quote, error) {
	return &Quote{
		FromCurrency: "USDC",
		ToCurrency:   toCurrency,
		FromAmount:   usdcAmount,
		ToAmount:     usdcAmount,
		Rate:         1,
	}, nil
}

// InitiateOnramp triggers an STK push: Safaricom prompts req.PhoneNumber's
// owner to enter their M-Pesa PIN to authorize the payment. Settlement is
// asynchronous — the immediate response only confirms the prompt was sent.
func (p *MPesaProvider) InitiateOnramp(ctx context.Context, req OnrampRequest) (*OnrampResult, error) {
	timestamp := time.Now().Format("20060102150405")
	body := map[string]any{
		"BusinessShortCode": p.cfg.Shortcode,
		"Password":          stkPassword(p.cfg.Shortcode, p.cfg.Passkey, timestamp),
		"Timestamp":         timestamp,
		"TransactionType":   "CustomerPayBillOnline",
		"Amount":            int64(req.Amount),
		"PartyA":            req.PhoneNumber,
		"PartyB":            p.cfg.Shortcode,
		"PhoneNumber":       req.PhoneNumber,
		"CallBackURL":       p.cfg.CallbackBaseURL + "/webhooks/mpesa/stk-callback",
		"AccountReference":  req.Reference,
		"TransactionDesc":   "Moistello USDC on-ramp",
	}

	var result struct {
		MerchantRequestID string `json:"MerchantRequestID"`
		CheckoutRequestID string `json:"CheckoutRequestID"`
		ResponseCode      string `json:"ResponseCode"`
		ResponseDesc      string `json:"ResponseDescription"`
	}
	if err := p.doRequest(ctx, "/mpesa/stkpush/v1/processrequest", body, &result); err != nil {
		return nil, err
	}
	if result.ResponseCode != "0" {
		return nil, fmt.Errorf("stk push rejected: %s", result.ResponseDesc)
	}

	return &OnrampResult{ProviderRef: result.CheckoutRequestID, Status: StatusPending}, nil
}

// InitiateOfframp triggers a B2C disbursement to the customer's phone.
func (p *MPesaProvider) InitiateOfframp(ctx context.Context, req OfframpRequest) (*OfframpResult, error) {
	body := map[string]any{
		"InitiatorName":      p.cfg.InitiatorName,
		"SecurityCredential": p.cfg.SecurityCredential,
		"CommandID":          "BusinessPayment",
		"Amount":             int64(req.Amount),
		"PartyA":             p.cfg.Shortcode,
		"PartyB":             req.PhoneNumber,
		"Remarks":            "Moistello USDC off-ramp",
		"QueueTimeOutURL":    p.cfg.CallbackBaseURL + "/webhooks/mpesa/b2c-timeout",
		"ResultURL":          p.cfg.CallbackBaseURL + "/webhooks/mpesa/b2c-result",
		"Occasion":           req.Reference,
	}

	var result struct {
		ConversationID           string `json:"ConversationID"`
		OriginatorConversationID string `json:"OriginatorConversationID"`
		ResponseCode             string `json:"ResponseCode"`
		ResponseDescription      string `json:"ResponseDescription"`
	}
	if err := p.doRequest(ctx, "/mpesa/b2c/v1/paymentrequest", body, &result); err != nil {
		return nil, err
	}
	if result.ResponseCode != "0" {
		return nil, fmt.Errorf("b2c payment rejected: %s", result.ResponseDescription)
	}

	return &OfframpResult{ProviderRef: result.ConversationID, Status: StatusPending}, nil
}

// GetStatus queries the STK push result for reconciliation when no
// callback has arrived yet (or as a defense-in-depth check even if one
// did). B2C results normally arrive only via ResultURL callback; Daraja has
// no generic polling endpoint for B2C, so GetStatus on a B2C ProviderRef
// returns StatusPending until the webhook updates it out-of-band.
func (p *MPesaProvider) GetStatus(ctx context.Context, providerRef string) (*StatusResult, error) {
	timestamp := time.Now().Format("20060102150405")
	body := map[string]any{
		"BusinessShortCode": p.cfg.Shortcode,
		"Password":          stkPassword(p.cfg.Shortcode, p.cfg.Passkey, timestamp),
		"Timestamp":         timestamp,
		"CheckoutRequestID": providerRef,
	}

	var result struct {
		ResultCode string `json:"ResultCode"`
		ResultDesc string `json:"ResultDesc"`
	}
	if err := p.doRequest(ctx, "/mpesa/stkpushquery/v1/query", body, &result); err != nil {
		// A query for an unknown/non-STK reference (e.g. a B2C
		// ProviderRef) fails rather than indicating status — treat as
		// still-pending so reconciliation just retries later.
		return &StatusResult{ProviderRef: providerRef, Status: StatusPending}, nil
	}

	switch result.ResultCode {
	case "0":
		return &StatusResult{ProviderRef: providerRef, Status: StatusCompleted}, nil
	case "1032":
		// Request cancelled by user.
		return &StatusResult{ProviderRef: providerRef, Status: StatusFailed, FailureReason: result.ResultDesc}, nil
	default:
		if result.ResultCode == "" {
			return &StatusResult{ProviderRef: providerRef, Status: StatusPending}, nil
		}
		return &StatusResult{ProviderRef: providerRef, Status: StatusFailed, FailureReason: result.ResultDesc}, nil
	}
}
