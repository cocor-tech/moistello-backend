// Package stellartest provides an in-process stand-in for Stellar Horizon and
// Soroban RPC so the infrastructure clients can be exercised offline. The
// mock speaks the same wire format as testnet for the endpoints the clients
// use (account lookup, transaction lookup, operations, sendTransaction,
// getTransaction, simulateTransaction) and records every request so tests
// can pin request shapes as well as response handling.
package stellartest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Balance mirrors one entry of a Horizon account's balances array.
type Balance struct {
	Balance     string `json:"balance"`
	AssetType   string `json:"asset_type"`
	AssetCode   string `json:"asset_code,omitempty"`
	AssetIssuer string `json:"asset_issuer,omitempty"`
}

// Account is a Horizon account record.
type Account struct {
	ID       string    `json:"id"`
	Sequence string    `json:"sequence"`
	Balances []Balance `json:"balances"`
}

// HorizonTransaction is a Horizon transaction record with its operations.
type HorizonTransaction struct {
	Hash       string
	Successful bool
	Operations []map[string]any
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// RPCResponse is a scripted reply to one JSON-RPC call. HTTPStatus defaults
// to 200. RawBody, when set, is written verbatim instead of a JSON-RPC
// envelope (used for malformed or non-JSON error bodies).
type RPCResponse struct {
	HTTPStatus int
	Result     any
	Error      *RPCError
	RawBody    string
}

// HorizonResponse is a scripted reply to the next Horizon request(s).
type HorizonResponse struct {
	HTTPStatus int
	Body       string
}

// Request is one recorded HTTP request.
type Request struct {
	Method string
	Path   string
	Body   string
	// RPCMethod and RPCParams are populated for JSON-RPC calls.
	RPCMethod string
	RPCParams json.RawMessage
}

// Network is the mock Horizon + Soroban RPC pair.
type Network struct {
	Horizon *httptest.Server
	RPC     *httptest.Server

	mu             sync.Mutex
	accounts       map[string]Account
	transactions   map[string]HorizonTransaction
	txStatuses     map[string][]map[string]any // getTransaction results per hash, consumed FIFO, last sticks
	rpcQueue       map[string][]RPCResponse    // scripted replies per method, consumed FIFO
	horizonQueue   []HorizonResponse           // scripted replies for upcoming Horizon requests
	requests       []Request
	simulateResult map[string]any
}

// New starts both mock servers. Call Close when done.
func New() *Network {
	n := &Network{
		accounts:     map[string]Account{},
		transactions: map[string]HorizonTransaction{},
		txStatuses:   map[string][]map[string]any{},
		rpcQueue:     map[string][]RPCResponse{},
		simulateResult: map[string]any{
			"transaction_data": "AAAA",
			"events":           []string{},
			"cost":             map[string]any{"cpu_instructions": 100000, "memory_bytes": 5000},
		},
	}
	n.Horizon = httptest.NewServer(http.HandlerFunc(n.serveHorizon))
	n.RPC = httptest.NewServer(http.HandlerFunc(n.serveRPC))
	return n
}

// Close shuts both servers down.
func (n *Network) Close() {
	n.Horizon.Close()
	n.RPC.Close()
}

// HorizonURL is the base URL of the mock Horizon server.
func (n *Network) HorizonURL() string { return n.Horizon.URL }

// RPCURL is the URL of the mock Soroban RPC server.
func (n *Network) RPCURL() string { return n.RPC.URL }

// SetAccount registers (or replaces) a Horizon account.
func (n *Network) SetAccount(address string, sequence int64, balances ...Balance) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if balances == nil {
		balances = []Balance{}
	}
	n.accounts[address] = Account{ID: address, Sequence: fmt.Sprintf("%d", sequence), Balances: balances}
}

// SetTransaction registers a Horizon transaction and its operation records.
func (n *Network) SetTransaction(tx HorizonTransaction) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.transactions[tx.Hash] = tx
}

// QueueHorizon scripts the reply for upcoming Horizon requests, in order.
// Once the queue is empty the mock answers from its state again.
func (n *Network) QueueHorizon(responses ...HorizonResponse) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.horizonQueue = append(n.horizonQueue, responses...)
}

// QueueRPC scripts replies for a JSON-RPC method, consumed in order. When
// the queue is empty the mock falls back to its default behaviour for that
// method.
func (n *Network) QueueRPC(method string, responses ...RPCResponse) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.rpcQueue[method] = append(n.rpcQueue[method], responses...)
}

// SetTransactionStatuses scripts successive getTransaction results for a
// hash, e.g. "NOT_FOUND", "NOT_FOUND", "SUCCESS". The last status repeats.
func (n *Network) SetTransactionStatuses(hash string, statuses ...string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	results := make([]map[string]any, 0, len(statuses))
	for _, s := range statuses {
		results = append(results, map[string]any{
			"status":      s,
			"result_xdr":  "AAAAAAAAAGQAAAAAAAAAAQAAAAAAAAABAAAAAAAAAAA=",
			"resultXdr":   "AAAAAAAAAGQAAAAAAAAAAQAAAAAAAAABAAAAAAAAAAA=",
			"ledger":      1234,
			"fee_charged": 100,
		})
	}
	n.txStatuses[hash] = results
}

// SetSimulateResult replaces the default simulateTransaction result.
func (n *Network) SetSimulateResult(result map[string]any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.simulateResult = result
}

// Requests returns every request received so far, in order.
func (n *Network) Requests() []Request {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Request, len(n.requests))
	copy(out, n.requests)
	return out
}

// RPCCalls returns the recorded params of every call to method.
func (n *Network) RPCCalls(method string) []json.RawMessage {
	var out []json.RawMessage
	for _, r := range n.Requests() {
		if r.RPCMethod == method {
			out = append(out, r.RPCParams)
		}
	}
	return out
}

// HorizonRequests returns the recorded Horizon requests only.
func (n *Network) HorizonRequests() []Request {
	var out []Request
	for _, r := range n.Requests() {
		if r.RPCMethod == "" {
			out = append(out, r)
		}
	}
	return out
}

// TxHash is the deterministic hash the mock assigns to a submitted envelope
// when no scripted sendTransaction reply is queued.
func TxHash(envelope string) string {
	sum := sha256.Sum256([]byte(envelope))
	return hex.EncodeToString(sum[:])
}

func (n *Network) serveHorizon(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	n.mu.Lock()
	n.requests = append(n.requests, Request{Method: r.Method, Path: r.URL.RequestURI(), Body: string(body)})
	if len(n.horizonQueue) > 0 {
		scripted := n.horizonQueue[0]
		n.horizonQueue = n.horizonQueue[1:]
		n.mu.Unlock()
		status := scripted.HTTPStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, scripted.Body)
		return
	}
	n.mu.Unlock()

	w.Header().Set("Content-Type", "application/hal+json")
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case len(parts) == 2 && parts[0] == "accounts":
		n.mu.Lock()
		acc, ok := n.accounts[parts[1]]
		n.mu.Unlock()
		if !ok {
			writeHorizonError(w, http.StatusNotFound, "Resource Missing")
			return
		}
		_ = json.NewEncoder(w).Encode(acc)
	case len(parts) == 2 && parts[0] == "transactions":
		n.mu.Lock()
		tx, ok := n.transactions[parts[1]]
		n.mu.Unlock()
		if !ok {
			writeHorizonError(w, http.StatusNotFound, "Resource Missing")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": tx.Hash, "hash": tx.Hash, "successful": tx.Successful, "ledger": 1234})
	case len(parts) == 3 && parts[0] == "transactions" && parts[2] == "operations":
		n.mu.Lock()
		tx, ok := n.transactions[parts[1]]
		n.mu.Unlock()
		if !ok {
			writeHorizonError(w, http.StatusNotFound, "Resource Missing")
			return
		}
		records := tx.Operations
		if records == nil {
			records = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"_embedded": map[string]any{"records": records}})
	default:
		writeHorizonError(w, http.StatusNotFound, "Resource Missing")
	}
}

func writeHorizonError(w http.ResponseWriter, status int, title string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "https://stellar.org/horizon-errors/not_found",
		"title":  title,
		"status": status,
		"detail": "The resource at the url requested was not found.",
	})
}

func (n *Network) serveRPC(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var call struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		ID     any             `json:"id"`
	}
	_ = json.Unmarshal(body, &call)

	n.mu.Lock()
	n.requests = append(n.requests, Request{Method: r.Method, Path: r.URL.RequestURI(), Body: string(body), RPCMethod: call.Method, RPCParams: call.Params})
	var scripted *RPCResponse
	if q := n.rpcQueue[call.Method]; len(q) > 0 {
		s := q[0]
		n.rpcQueue[call.Method] = q[1:]
		scripted = &s
	}
	n.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if scripted != nil {
		status := scripted.HTTPStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		if scripted.RawBody != "" {
			_, _ = io.WriteString(w, scripted.RawBody)
			return
		}
		writeRPC(w, call.ID, scripted.Result, scripted.Error)
		return
	}

	switch call.Method {
	case "sendTransaction":
		var p struct {
			Transaction string `json:"transaction"`
		}
		_ = json.Unmarshal(call.Params, &p)
		writeRPC(w, call.ID, map[string]any{"hash": TxHash(p.Transaction), "status": "PENDING", "latestLedger": 1234}, nil)
	case "getTransaction":
		var p struct {
			Hash string `json:"hash"`
		}
		_ = json.Unmarshal(call.Params, &p)
		n.mu.Lock()
		statuses := n.txStatuses[p.Hash]
		var result map[string]any
		switch len(statuses) {
		case 0:
			result = map[string]any{"status": "NOT_FOUND", "latestLedger": 1234}
		case 1:
			result = statuses[0]
		default:
			result = statuses[0]
			n.txStatuses[p.Hash] = statuses[1:]
		}
		n.mu.Unlock()
		writeRPC(w, call.ID, result, nil)
	case "simulateTransaction":
		n.mu.Lock()
		result := n.simulateResult
		n.mu.Unlock()
		writeRPC(w, call.ID, result, nil)
	default:
		writeRPC(w, call.ID, nil, &RPCError{Code: -32601, Message: "method not found: " + call.Method})
	}
}

func writeRPC(w http.ResponseWriter, id any, result any, rpcErr *RPCError) {
	if id == nil {
		id = 1
	}
	envelope := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		envelope["error"] = rpcErr
	} else {
		envelope["result"] = result
	}
	_ = json.NewEncoder(w).Encode(envelope)
}
