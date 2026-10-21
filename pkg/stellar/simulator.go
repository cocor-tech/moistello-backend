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

// SimulationResult is the result of simulating a transaction
type SimulationResult struct {
	TransactionData string       `json:"transaction_data"`
	ReturnValue     any          `json:"returnValue,omitempty"`
	Events          []string     `json:"events"`
	Cost            SimulateCost `json:"cost"`
	Error           *string      `json:"error,omitempty"`
}

type SimulateCost struct {
	CPUInstructions uint64 `json:"cpu_instructions"`
	MemoryBytes     uint64 `json:"memory_bytes"`
}

// Simulator runs pre-flight simulations on Soroban RPC
type Simulator struct {
	rpcURL     string
	httpClient *http.Client
}

func NewSimulator(rpcURL string) *Simulator {
	return &Simulator{
		rpcURL:     rpcURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// SimulateTransaction runs a pre-flight simulation without spending gas.
// Catches errors BEFORE submitting to the network.
func (s *Simulator) SimulateTransaction(ctx context.Context, tx *Transaction) (*SimulationResult, error) {
	txJSON, err := tx.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("marshaling transaction: %w", err)
	}

	body := fmt.Sprintf(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "simulateTransaction",
		"params": {"transaction": %s}
	}`, string(txJSON))

	req, err := http.NewRequestWithContext(ctx, "POST", s.rpcURL, bytes.NewBufferString(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("simulation request: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Warn().Err(cerr).Msg("failed to close simulation response body")
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading simulation response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, ClassifyError(resp.StatusCode, respBody)
	}

	var rpcResp struct {
		Result SimulationResult `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("decoding simulation response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return &rpcResp.Result, nil
}

// ApplyResources applies the resource estimates from simulation to the transaction
func (s *Simulator) ApplyResources(tx *Transaction, result *SimulationResult) *Transaction {
	costFactor := int64(result.Cost.CPUInstructions/10000 + result.Cost.MemoryBytes/100)
	if costFactor > 1000 {
		costFactor = 1000
	}
	tx.Fee = tx.Fee * costFactor
	if tx.Fee < 100 {
		tx.Fee = 100
	}
	return tx
}
