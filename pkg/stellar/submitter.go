package stellar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// SubmitConfig defines retry behavior for transaction submission
type SubmitConfig struct {
	MaxAttempts int
	Backoff     []time.Duration
	Timeout     time.Duration
}

// DefaultSubmitConfig returns sensible defaults
func DefaultSubmitConfig() SubmitConfig {
	return SubmitConfig{
		MaxAttempts: 3,
		Backoff:     []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second},
		Timeout:     45 * time.Second,
	}
}

// PollConfig defines polling behavior for transaction confirmation
type PollConfig struct {
	Interval time.Duration
	Timeout  time.Duration
}

func DefaultPollConfig() PollConfig {
	return PollConfig{
		Interval: 2 * time.Second,
		Timeout:  60 * time.Second,
	}
}

// TransactionResult is the final result of a submitted transaction
type TransactionResult struct {
	Hash       string
	Successful bool
	ResultXDR  string
	Ledger     int64
	FeeCharged int64
}

// Submitter handles transaction submission + polling
type Submitter struct {
	rpcURL     string
	horizonURL string
	httpClient *http.Client
}

func NewSubmitter(rpcURL, horizonURL string) *Submitter {
	return &Submitter{
		rpcURL:     rpcURL,
		horizonURL: horizonURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// SubmitWithRetry submits a signed transaction with exponential backoff retry
func (s *Submitter) SubmitWithRetry(ctx context.Context, signedTx string, cfg SubmitConfig) (string, error) {
	var lastErr error

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		if attempt > 0 {
			backoffIdx := attempt - 1
			if backoffIdx >= len(cfg.Backoff) {
				backoffIdx = len(cfg.Backoff) - 1
			}
			select {
			case <-time.After(cfg.Backoff[backoffIdx]):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}

		txHash, err := s.submit(ctx, signedTx)
		if err == nil {
			return txHash, nil
		}

		if !IsRetryable(err) {
			return "", err
		}

		lastErr = err
		log.Warn().Err(err).Int("attempt", attempt+1).Msg("retrying transaction submission")
	}

	return "", fmt.Errorf("submission failed after %d attempts: %w", cfg.MaxAttempts, lastErr)
}

func (s *Submitter) submit(ctx context.Context, signedTx string) (string, error) {
	body := fmt.Sprintf(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "sendTransaction",
		"params": {"transaction": "%s"}
	}`, signedTx)

	req, err := http.NewRequestWithContext(ctx, "POST", s.rpcURL, bytes.NewBufferString(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("submit request: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Warn().Err(cerr).Msg("failed to close submit response body")
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading submit response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", ClassifyError(resp.StatusCode, respBody)
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
		return "", fmt.Errorf("decoding submit response: %w", err)
	}

	if rpcResp.Error != nil {
		return "", ClassifyError(resp.StatusCode, []byte(rpcResp.Error.Message))
	}

	if rpcResp.Result.Status == "ERROR" {
		return "", fmt.Errorf("transaction rejected: %s", rpcResp.Result.Hash)
	}

	return rpcResp.Result.Hash, nil
}

// PollUntilFinal polls for transaction completion
func (s *Submitter) PollUntilFinal(ctx context.Context, txHash string, cfg PollConfig) (*TransactionResult, error) {
	deadline := time.Now().Add(cfg.Timeout)

	for time.Now().Before(deadline) {
		result, err := s.getTransactionStatus(ctx, txHash)
		if err != nil {
			return nil, err
		}

		switch result.Status {
		case "SUCCESS":
			return &TransactionResult{
				Hash:       txHash,
				Successful: true,
				ResultXDR:  result.ResultXDR,
				Ledger:     result.Ledger,
				FeeCharged: result.FeeCharged,
			}, nil
		case "FAILED":
			return &TransactionResult{
				Hash:       txHash,
				Successful: false,
				ResultXDR:  result.ResultXDR,
				Ledger:     result.Ledger,
				FeeCharged: result.FeeCharged,
			}, fmt.Errorf("transaction failed: %s", result.ResultXDR)
		}

		select {
		case <-time.After(cfg.Interval):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return nil, fmt.Errorf("transaction %s not confirmed after %v", txHash, cfg.Timeout)
}

type txStatusResult struct {
	Status     string `json:"status"`
	ResultXDR  string `json:"result_xdr"`
	Ledger     int64  `json:"ledger"`
	FeeCharged int64  `json:"fee_charged"`
}

func (s *Submitter) getTransactionStatus(ctx context.Context, txHash string) (*txStatusResult, error) {
	body := fmt.Sprintf(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "getTransaction",
		"params": {"hash": "%s"}
	}`, txHash)

	req, err := http.NewRequestWithContext(ctx, "POST", s.rpcURL, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Warn().Err(cerr).Msg("failed to close getTransaction response body")
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rpcResp struct {
		Result struct {
			Status     string `json:"status"`
			ResultXDR  string `json:"result_xdr"`
			Ledger     int64  `json:"ledger"`
			FeeCharged int64  `json:"fee_charged"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("decoding tx status: %w", err)
	}

	return &txStatusResult{
		Status:     rpcResp.Result.Status,
		ResultXDR:  rpcResp.Result.ResultXDR,
		Ledger:     rpcResp.Result.Ledger,
		FeeCharged: rpcResp.Result.FeeCharged,
	}, nil
}
