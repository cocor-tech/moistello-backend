package soroban

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stellar/go/strkey"
	"github.com/stellar/go/xdr"
	"go.opentelemetry.io/otel/attribute"

	"github.com/moistello/backend/pkg/tracing"
)

// contractIDSize is the byte length of a Soroban contract ID, which is a
// 32-byte hash regardless of whether it is written as strkey or hex.
const contractIDSize = 32

// ErrContractNotFound is returned when the RPC node has no contract instance
// ledger entry for the requested contract, which means the contract does not
// exist at the queried ledger.
var ErrContractNotFound = errors.New("contract instance not found on ledger")

// LedgerClient reads live ledger state from the Soroban RPC endpoint.
type LedgerClient struct {
	rpcURL     string
	httpClient *http.Client
}

// NewLedgerClient creates a client for the Soroban RPC endpoint at rpcURL.
func NewLedgerClient(rpcURL string) *LedgerClient {
	return &LedgerClient{
		rpcURL:     rpcURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// GetContractWasmHash returns the hex-encoded executable (WASM) hash of the
// contract identified by contractID, which is the stable identifier of the code
// a contract is currently running.
//
// contractID may be given either as a strkey "C..." address or as the bare
// 32-byte hex the indexer decodes contract addresses to; both forms are
// accepted so callers do not have to convert first.
//
// The hash is read from the contract instance ledger entry via getLedgerEntries.
// It reflects the contract's executable at the ledger the RPC node serves,
// which is the most recent one, so a caller correlating this with a historical
// event should treat it as the version the contract runs now rather than a
// guaranteed historical guarantee.
func (c *LedgerClient) GetContractWasmHash(ctx context.Context, contractID string) (version string, err error) {
	start := time.Now()

	addr, err := parseContractAddress(contractID)
	if err != nil {
		return "", err
	}

	key, err := xdr.MarshalBase64(contractInstanceKey(addr))
	if err != nil {
		return "", fmt.Errorf("marshaling contract instance key: %w", err)
	}

	// The span records the operation only. The requested ledger key identifies a
	// contract, and the response a hash, so neither is user data; the RPC URL is
	// deliberately not recorded at all.
	ctx, span := tracing.StartStellarSpan(ctx, "getLedgerEntries")
	span.SetAttributes(attribute.String("rpc.method", "getLedgerEntries"))
	defer func() { tracing.EndSpan(span, err, start) }()

	body := fmt.Sprintf(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "getLedgerEntries",
		"params": {"keys": [%q]}
	}`, key)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewBufferString(body))
	if err != nil {
		return "", fmt.Errorf("creating getLedgerEntries request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("getLedgerEntries request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading getLedgerEntries response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("getLedgerEntries error %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp struct {
		Result struct {
			Entries []struct {
				XDR string `json:"xdr"`
			} `json:"entries"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return "", fmt.Errorf("decoding getLedgerEntries response: %w", err)
	}
	if rpcResp.Error != nil {
		return "", fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if len(rpcResp.Result.Entries) == 0 {
		return "", fmt.Errorf("%w: %s", ErrContractNotFound, contractID)
	}

	raw, err := decodeXDRBase64(rpcResp.Result.Entries[0].XDR)
	if err != nil {
		return "", fmt.Errorf("decoding ledger entry xdr: %w", err)
	}

	var entry xdr.LedgerEntryData
	if _, err := xdr.Unmarshal(bytes.NewReader(raw), &entry); err != nil {
		return "", fmt.Errorf("xdr unmarshal LedgerEntryData: %w", err)
	}
	if entry.ContractData == nil {
		return "", fmt.Errorf("%w: entry is not contract data: %s", ErrContractNotFound, contractID)
	}

	// A contract instance ledger entry is keyed by the void
	// ScvLedgerKeyContractInstance marker and carries the instance itself in its
	// value, so the executable hash is read from the value.
	instance := entry.ContractData.Val.Instance
	if instance == nil {
		return "", fmt.Errorf("%w: entry carries no contract instance: %s", ErrContractNotFound, contractID)
	}
	// A contract that is a Stellar asset rather than Wasm has no WASM hash, so
	// there is no version to report for it.
	if instance.Executable.WasmHash == nil {
		return "", fmt.Errorf("%w: no wasm hash: %s", ErrContractNotFound, contractID)
	}

	return hex.EncodeToString(instance.Executable.WasmHash[:]), nil
}

// contractInstanceKey builds the ledger key of a contract's instance entry.
//
// The key is the contract's single persistent entry whose key and value are
// both ScvLedgerKeyContractInstance; the storage map is left nil because only
// the entry's key is addressed here, not its contents.
func contractInstanceKey(addr xdr.ScAddress) xdr.LedgerKey {
	return xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeContractData,
		ContractData: &xdr.LedgerKeyContractData{
			Contract: addr,
			Key: xdr.ScVal{
				Type:     xdr.ScValTypeScvLedgerKeyContractInstance,
				Instance: &xdr.ScContractInstance{},
			},
			Durability: xdr.ContractDataDurabilityPersistent,
		},
	}
}

// parseContractAddress converts either a strkey "C..." contract address or a
// bare 32-byte hex string into an ScAddress. The indexer decodes contract
// addresses to hex, while configuration holds strkey, so both forms are
// accepted at this boundary.
func parseContractAddress(s string) (xdr.ScAddress, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return xdr.ScAddress{}, errors.New("empty contract id")
	}

	if raw, err := hex.DecodeString(s); err == nil && len(raw) == contractIDSize {
		var id xdr.ContractId
		copy(id[:], raw)
		return contractAddress(id), nil
	}

	raw, err := strkey.Decode(strkey.VersionByteContract, s)
	if err != nil {
		return xdr.ScAddress{}, fmt.Errorf("invalid contract id %q: %w", s, err)
	}
	if len(raw) != contractIDSize {
		return xdr.ScAddress{}, fmt.Errorf("invalid contract id %q: unexpected length %d", s, len(raw))
	}
	var id xdr.ContractId
	copy(id[:], raw)
	return contractAddress(id), nil
}

func contractAddress(id xdr.ContractId) xdr.ScAddress {
	return xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &id,
	}
}

// decodeXDRBase64 decodes base64 XDR, tolerating both the padded standard
// alphabet and the unpadded URL-safe one that RPC nodes are known to emit.
func decodeXDRBase64(s string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		raw, err := enc.DecodeString(s)
		if err == nil {
			return raw, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
