package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Poller fetches ledgers and transactions from the Stellar Horizon API.
type Poller struct {
	horizonURL  string
	contractIDs []string
	httpClient  *http.Client
}

// LedgerResponse is the Horizon API response for ledgers.
type LedgerResponse struct {
	Embedded struct {
		Records []Ledger `json:"records"`
	} `json:"_embedded"`
}

// Ledger represents a single Stellar ledger.
type Ledger struct {
	Sequence int64     `json:"sequence"`
	ClosedAt time.Time `json:"closed_at"`
	TxCount  int       `json:"transaction_count"`
	// Hash is the ledger header hash, and PrevHash the hash of the ledger that
	// preceded it on the branch this ledger belongs to. Comparing these against
	// the values recorded when the ledger was first indexed is how the indexer
	// detects a reorg (#346).
	Hash     string `json:"hash"`
	PrevHash string `json:"prev_hash"`
}

// TransactionResponse is the Horizon API response for transactions.
type TransactionResponse struct {
	Embedded struct {
		Records []Transaction `json:"records"`
	} `json:"_embedded"`
}

// Transaction represents a Stellar transaction.
type Transaction struct {
	Hash          string      `json:"hash"`
	Ledger        int64       `json:"ledger"`
	CreatedAt     time.Time   `json:"created_at"`
	SourceAccount string      `json:"source_account"`
	Successful    bool        `json:"successful"`
	Operations    []Operation `json:"operations"`
	// LedgerCloseTime is the close time of the ledger containing this
	// transaction, set by the engine from the Horizon ledger response.
	// Used instead of time.Now() to ensure timestamps follow ledger
	// sequence ordering (#471).
	LedgerCloseTime time.Time `json:"-"`
}

// Operation represents a single operation within a Stellar transaction.
// ContractID is populated for Soroban contract invocations from the
// operation's function / contract reference when it is available.
type Operation struct {
	ID            int64  `json:"id"`
	Type          string `json:"type"`
	SourceAccount string `json:"source_account"`
	ContractID    string `json:"contract_id,omitempty"`
	// ResultMetaXDR is the base64-encoded TransactionMeta XDR from Horizon.
	// Populated for invoke_host_function operations and used by the XDR parser
	// to extract Soroban contract events (ContractEvent topic/data SCVals).
	ResultMetaXDR string `json:"result_meta_xdr"`
}

// NewPoller creates a Poller that queries the given Horizon URL and filters
// transactions against the provided contract IDs.
func NewPoller(horizonURL string, contractIDs []string) *Poller {
	return &Poller{
		horizonURL:  horizonURL,
		contractIDs: contractIDs,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

// FetchLedgers retrieves ledgers after the given cursor, up to the specified limit.
func (p *Poller) FetchLedgers(ctx context.Context, cursor int64, limit int) ([]Ledger, error) {
	pagingToken := cursor
	if pagingToken > 0 {
		pagingToken = pagingToken << 32
	}
	url := fmt.Sprintf("%s/ledgers?order=asc&limit=%d&cursor=%d", p.horizonURL, limit, pagingToken)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching ledgers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("horizon error %d: %s", resp.StatusCode, string(body))
	}

	var result LedgerResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding ledgers: %w", err)
	}
	return result.Embedded.Records, nil
}

// FetchLedgersInRange retrieves the ledgers in the inclusive range [from, to].
// It is used by the reorg check to re-read the recent window of already
// indexed ledgers and compare their hashes against what was recorded.
func (p *Poller) FetchLedgersInRange(ctx context.Context, from, to int64) ([]Ledger, error) {
	if to < from {
		return nil, nil
	}
	return p.FetchLedgers(ctx, from-1, int(to-from+1))
}

// FetchTransactions retrieves all transactions for a specific ledger.
func (p *Poller) FetchTransactions(ctx context.Context, ledger int64) ([]Transaction, error) {
	url := fmt.Sprintf("%s/ledgers/%d/transactions?limit=200", p.horizonURL, ledger)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching transactions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("horizon error %d: %s", resp.StatusCode, string(body))
	}

	var result TransactionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding transactions: %w", err)
	}

	return result.Embedded.Records, nil
}

// FilterByContract keeps only transactions that reference one of the configured
// contract IDs. A transaction is a match if any of its operations originates
// from a configured account (e.g. the master public key) or invokes one of the
// configured contracts. It deliberately does NOT match every invoke_host_function
// operation — only operations against the specific contracts being indexed.
func (p *Poller) FilterByContract(txns []Transaction) []Transaction {
	if len(p.contractIDs) == 0 {
		return txns
	}

	wanted := make(map[string]struct{}, len(p.contractIDs))
	for _, cid := range p.contractIDs {
		wanted[cid] = struct{}{}
	}

	var filtered []Transaction
	for _, txn := range txns {
		if txn.MatchesContract(wanted) {
			filtered = append(filtered, txn)
		}
	}
	return filtered
}

// MatchesContract reports whether any operation in the transaction originates
// from or targets one of the provided contract identifiers.
func (t Transaction) MatchesContract(wanted map[string]struct{}) bool {
	for _, op := range t.Operations {
		if _, ok := wanted[op.SourceAccount]; ok {
			return true
		}
		if op.ContractID != "" {
			if _, ok := wanted[op.ContractID]; ok {
				return true
			}
		}
	}
	return false
}

// GetLedgerCount returns the number of new ledgers available after the cursor.
func (p *Poller) GetLedgerCount(ctx context.Context, cursor int64) (int, error) {
	ledgers, err := p.FetchLedgers(ctx, cursor, 1)
	if err != nil {
		return 0, err
	}
	if len(ledgers) == 0 {
		return 0, nil
	}
	return int(ledgers[0].Sequence - cursor), nil
}

// HorizonURL returns the configured Horizon API base URL.
func (p *Poller) HorizonURL() string { return p.horizonURL }

// ContractIDs returns the configured contract IDs for filtering.
func (p *Poller) ContractIDs() []string { return p.contractIDs }
