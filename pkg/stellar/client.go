package stellar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/moistello/backend/pkg/tracing"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
)

type Client struct {
	horizonURL        string
	sorobanRPCURL     string
	networkPassphrase string
	httpClient        *http.Client
	cb                *CircuitBreaker
}

func NewClient(horizonURL, sorobanRPCURL, networkPassphrase string) *Client {
	return &Client{
		horizonURL:        horizonURL,
		sorobanRPCURL:     sorobanRPCURL,
		networkPassphrase: networkPassphrase,
		httpClient:        &http.Client{Timeout: 30 * time.Second},
		cb:                NewCircuitBreaker("horizon", DefaultCircuitBreakerConfig()),
	}
}

type HorizonAccountResponse struct {
	ID       string `json:"id"`
	Sequence string `json:"sequence"`
	Balances []struct {
		Balance     string `json:"balance"`
		AssetType   string `json:"asset_type"`
		AssetCode   string `json:"asset_code"`
		AssetIssuer string `json:"asset_issuer"`
	} `json:"balances"`
}

func (c *Client) GetAccount(ctx context.Context, address string) (account *HorizonAccountResponse, err error) {
	ctx, span := tracing.StartStellarSpan(ctx, "get_account")
	start := time.Now()
	defer func() { tracing.EndSpan(span, err, start, attribute.String("stellar.address", address)) }()

	err = c.cb.Execute(ctx, func() error {
		url := fmt.Sprintf("%s/accounts/%s", c.horizonURL, address)
		resp, err := c.httpClient.Get(url)
		if err != nil {
			return fmt.Errorf("horizon request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("account not found")
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("horizon error %d: %s", resp.StatusCode, string(body))
		}

		var a HorizonAccountResponse
		if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
			return fmt.Errorf("decoding horizon response: %w", err)
		}
		account = &a
		return nil
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

func (c *Client) GetTransaction(ctx context.Context, txnHash string) (result map[string]any, err error) {
	ctx, span := tracing.StartStellarSpan(ctx, "get_transaction")
	start := time.Now()
	defer func() { tracing.EndSpan(span, err, start, attribute.String("stellar.txn_hash", txnHash)) }()

	err = c.cb.Execute(ctx, func() error {
		url := fmt.Sprintf("%s/transactions/%s", c.horizonURL, txnHash)
		resp, err := c.httpClient.Get(url)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("horizon error %d", resp.StatusCode)
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// VerifyTransaction checks that a transaction exists, was successful, and
// contains a payment operation where the sender matches expectedFrom AND the
// amount matches expectedAmount. Pass an empty string to skip a check.
func (c *Client) VerifyTransaction(ctx context.Context, txnHash string, expectedFrom string, expectedAmount string) (bool, error) {
	ops, err := c.getTransactionOperations(ctx, txnHash)
	if err != nil {
		return false, err
	}
	for _, r := range ops {
		from, _ := r["from"].(string)
		amount, _ := r["amount"].(string)
		if expectedFrom != "" && from != expectedFrom {
			continue
		}
		if expectedAmount != "" && amount != expectedAmount {
			continue
		}
		return true, nil
	}
	return false, nil
}

// VerifyPayment checks that a transaction exists, was successful, and contains
// a payment operation where recipient matches expectedTo AND amount matches
// expectedAmount. Pass an empty string to skip a check.
func (c *Client) VerifyPayment(ctx context.Context, txnHash string, expectedTo string, expectedAmount string) (bool, error) {
	ops, err := c.getTransactionOperations(ctx, txnHash)
	if err != nil {
		return false, err
	}
	for _, r := range ops {
		to, _ := r["to"].(string)
		amount, _ := r["amount"].(string)
		if expectedTo != "" && to != expectedTo {
			continue
		}
		if expectedAmount != "" && amount != expectedAmount {
			continue
		}
		return true, nil
	}
	return false, nil
}

// getTransactionOperations fetches a transaction from Horizon, asserts it was
// successful, then returns its payment operation records.
func (c *Client) getTransactionOperations(ctx context.Context, txnHash string) ([]map[string]any, error) {
	txn, err := c.GetTransaction(ctx, txnHash)
	if err != nil {
		log.Warn().Err(err).Str("txn", txnHash).Msg("failed to fetch transaction from horizon")
		return nil, err
	}
	if success, ok := txn["successful"].(bool); ok && !success {
		return nil, fmt.Errorf("transaction %s was not successful on-chain", txnHash)
	}

	url := fmt.Sprintf("%s/transactions/%s/operations?limit=200", c.horizonURL, txnHash)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("horizon request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("horizon error %d: %s", resp.StatusCode, string(body))
	}

	var envelope struct {
		Embedded struct {
			Records []map[string]any `json:"records"`
		} `json:"_embedded"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decoding horizon operations: %w", err)
	}

	// Filter to payment-type operations only
	var payments []map[string]any
	for _, r := range envelope.Embedded.Records {
		typ, _ := r["type"].(string)
		if typ == "payment" || typ == "payment_strict_receive" || typ == "payment_strict_send" {
			payments = append(payments, r)
		}
	}
	return payments, nil
}

func (c *Client) NetworkPassphrase() string { return c.networkPassphrase }
func (c *Client) HorizonURL() string        { return c.horizonURL }
func (c *Client) SorobanRPCURL() string     { return c.sorobanRPCURL }
