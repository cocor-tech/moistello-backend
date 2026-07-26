package soroban

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/moistello/backend/pkg/stellar"
)

// Client wraps the Soroban RPC endpoint for contract operations.
type Client struct {
	rpcURL     string
	httpClient *http.Client
}

func NewClient(rpcURL string) *Client {
	return &Client{
		rpcURL:     rpcURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// GetAccount fetches account info from Horizon via the parent stellar client.
func (c *Client) GetAccount(ctx context.Context, address string) (*stellar.HorizonAccountResponse, error) {
	hc := stellar.NewClient("", c.rpcURL, "")
	resp, err := hc.GetAccount(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("soroban: get account %s: %w", address, err)
	}
	return resp, nil
}

// GetTransaction fetches transaction details from Soroban RPC.
func (c *Client) GetTransaction(ctx context.Context, hash string) (map[string]any, error) {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"getTransaction","params":{"hash":"%s"}}`, hash)
	return c.rpcCall(ctx, body)
}

// SimulateTransaction runs pre-flight simulation using the parent Simulator.
func (c *Client) SimulateTransaction(ctx context.Context, tx *stellar.Transaction) (*stellar.SimulationResult, error) {
	sim := stellar.NewSimulator(c.rpcURL)
	result, err := sim.SimulateTransaction(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("soroban: simulate: %w", err)
	}
	return result, nil
}

// SendTransaction submits a signed transaction envelope and polls for finality.
func (c *Client) SendTransaction(ctx context.Context, signedTxEnvelope string) (*TxResult, error) {
	submitter := &txSubmitter{
		rpcURL:     c.rpcURL,
		httpClient: c.httpClient,
	}
	return submitter.submitAndPoll(ctx, signedTxEnvelope, DefaultSubmitConfig(), DefaultPollConfig())
}

func (c *Client) rpcCall(ctx context.Context, body string) (map[string]any, error) {
	var result map[string]any
	err := stellar.ExecuteWithBackoff(ctx, "soroban_rpcCall", func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, "POST", c.rpcURL, bytes.NewBufferString(body))
		if err != nil {
			return fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("rpc request: %w", err)
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("reading rpc response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return stellar.ClassifyError(resp.StatusCode, respBody)
		}

		var res map[string]any
		if err := json.Unmarshal(respBody, &res); err != nil {
			return fmt.Errorf("decoding rpc response: %w", err)
		}
		result = res
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) RPCURL() string { return c.rpcURL }

// ── Submit / Poll Configuration ──

type SubmitConfig struct {
	MaxRetries int
	RetryDelay time.Duration
}

type PollConfig struct {
	MaxAttempts int
	Interval    time.Duration
}

func DefaultSubmitConfig() SubmitConfig {
	return SubmitConfig{
		MaxRetries: 5,
		RetryDelay: 2 * time.Second,
	}
}

func DefaultPollConfig() PollConfig {
	return PollConfig{
		MaxAttempts: 60,
		Interval:    1 * time.Second,
	}
}

// ── TxResult ──

type TxResult struct {
	Hash       string
	Successful bool
	ResultXDR  string
	Ledger     int64
}

// ── txSubmitter ──

type txSubmitter struct {
	rpcURL     string
	httpClient *http.Client
}

func (s *txSubmitter) submitAndPoll(ctx context.Context, signedTxEnvelope string, subCfg SubmitConfig, pollCfg PollConfig) (*TxResult, error) {
	var lastErr error
	for attempt := 0; attempt <= subCfg.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(subCfg.RetryDelay):
			}
		}

		txHash, err := s.sendTransaction(ctx, signedTxEnvelope)
		if err != nil {
			if txErr, ok := err.(*stellar.TransactionError); ok && txErr.IsRetryable {
				lastErr = err
				continue
			}
			return nil, err
		}

		result, err := s.pollUntilFinal(ctx, txHash, pollCfg)
		if err != nil {
			if stellar.IsRetryable(err) {
				lastErr = err
				continue
			}
			return nil, err
		}
		return result, nil
	}
	return nil, fmt.Errorf("submit failed after %d retries: %w", subCfg.MaxRetries, lastErr)
}

func (s *txSubmitter) sendTransaction(ctx context.Context, signedTxEnvelope string) (string, error) {
	body := fmt.Sprintf(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "sendTransaction",
		"params": {"transaction": "%s"}
	}`, signedTxEnvelope)

	req, err := http.NewRequestWithContext(ctx, "POST", s.rpcURL, bytes.NewBufferString(body))
	if err != nil {
		return "", fmt.Errorf("creating send request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send transaction: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading send response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", stellar.ClassifyError(resp.StatusCode, respBody)
	}

	var rpcResp struct {
		Result struct {
			Hash   string `json:"hash"`
			Status string `json:"status"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return "", fmt.Errorf("decoding send response: %w", err)
	}
	if rpcResp.Error != nil {
		return "", fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if rpcResp.Result.Hash == "" {
		return "", fmt.Errorf("no transaction hash in response")
	}
	return rpcResp.Result.Hash, nil
}

func (s *txSubmitter) pollUntilFinal(ctx context.Context, txHash string, cfg PollConfig) (*TxResult, error) {
	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(cfg.Interval):
		}

		result, err := s.getTransactionStatus(ctx, txHash)
		if err != nil {
			return nil, err
		}

		switch result.Status {
		case "SUCCESS":
			return &TxResult{
				Hash:       txHash,
				Successful: true,
				ResultXDR:  result.ResultXDR,
				Ledger:     result.Ledger,
			}, nil
		case "FAILED":
			return &TxResult{
				Hash:       txHash,
				Successful: false,
				ResultXDR:  result.ResultXDR,
				Ledger:     result.Ledger,
			}, nil
		case "NOT_FOUND":
			continue
		default:
			continue
		}
	}
	return nil, fmt.Errorf("transaction %s not final after %d attempts", txHash, cfg.MaxAttempts)
}

type txStatusResult struct {
	Status    string
	ResultXDR string
	Ledger    int64
}

func (s *txSubmitter) getTransactionStatus(ctx context.Context, txHash string) (*txStatusResult, error) {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"getTransaction","params":{"hash":"%s"}}`, txHash)

	req, err := http.NewRequestWithContext(ctx, "POST", s.rpcURL, bytes.NewBufferString(body))
	if err != nil {
		return nil, fmt.Errorf("creating poll request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading poll response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, stellar.ClassifyError(resp.StatusCode, respBody)
	}

	var rpcResp struct {
		Result struct {
			Status        string `json:"status"`
			ResultXdr     string `json:"resultXdr"`
			Ledger        int64  `json:"ledger"`
			ApplicationOrder int64 `json:"applicationOrder"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("decoding poll response: %w", err)
	}

	return &txStatusResult{
		Status:    rpcResp.Result.Status,
		ResultXDR: rpcResp.Result.ResultXdr,
		Ledger:    rpcResp.Result.Ledger,
	}, nil
}
