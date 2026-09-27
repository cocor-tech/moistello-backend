package soroban_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stellar/go/strkey"
	"github.com/stellar/go/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/stellar/soroban"
)

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// testContractID is a valid 32-byte contract ID, written as strkey.
var testContractID = strkey.MustEncode(strkey.VersionByteContract, bytes32(0xAB))

func bytes32(fill byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = fill
	}
	return b
}

func contractAddress(id string) xdr.ScAddress {
	raw, err := strkey.Decode(strkey.VersionByteContract, id)
	if err != nil {
		panic(err)
	}
	var cid xdr.ContractId
	copy(cid[:], raw)
	return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &cid}
}

// wasmInstanceEntry builds the ledger entry an RPC node returns for a contract
// instance whose executable is the given WASM hash.
func wasmInstanceEntry(t *testing.T, contractID string, wasmHash []byte) string {
	t.Helper()

	var hash xdr.Hash
	copy(hash[:], wasmHash)

	entry := xdr.LedgerEntryData{
		Type: xdr.LedgerEntryTypeContractData,
		ContractData: &xdr.ContractDataEntry{
			Contract:   contractAddress(contractID),
			Durability: xdr.ContractDataDurabilityPersistent,
			Key: xdr.ScVal{
				Type:     xdr.ScValTypeScvLedgerKeyContractInstance,
				Instance: &xdr.ScContractInstance{},
			},
			// The entry's key is the ScvLedgerKeyContractInstance marker; its
			// value is the instance itself, carried as ScvContractInstance.
			Val: xdr.ScVal{
				Type: xdr.ScValTypeScvContractInstance,
				Instance: &xdr.ScContractInstance{
					Executable: xdr.ContractExecutable{
						Type:     xdr.ContractExecutableTypeContractExecutableWasm,
						WasmHash: &hash,
					},
				},
			},
		},
	}

	encoded, err := xdr.MarshalBase64(entry)
	require.NoError(t, err)
	return encoded
}

// serveEntries starts a stub RPC node returning the given raw JSON body.
func serveEntries(t *testing.T, body string) *soroban.LedgerClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return soroban.NewLedgerClient(srv.URL)
}

func entriesBody(t *testing.T, xdrB64 string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]any{
			"entries": []map[string]any{
				{"key": "ignored", "xdr": xdrB64, "lastModifiedLedgerSeq": 100},
			},
			"latestLedger": 101,
		},
	})
	require.NoError(t, err)
	return string(body)
}

// TestGetContractWasmHash is the core case: the WASM hash is read out of the
// contract instance ledger entry and returned hex encoded.
func TestGetContractWasmHash(t *testing.T) {
	wasmHash := bytes32(0xCD)
	client := serveEntries(t, entriesBody(t, wasmInstanceEntry(t, testContractID, wasmHash)))

	version, err := client.GetContractWasmHash(context.Background(), testContractID)
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(wasmHash), version)
}

// TestGetContractWasmHash_AcceptsHexContractID covers the form the indexer
// actually stores: contract addresses are decoded to bare hex, so a resolver
// handed one of those must still work.
func TestGetContractWasmHash_AcceptsHexContractID(t *testing.T) {
	wasmHash := bytes32(0x11)
	client := serveEntries(t, entriesBody(t, wasmInstanceEntry(t, testContractID, wasmHash)))

	hexID := hex.EncodeToString(mustDecodeStrkey(t, testContractID))
	version, err := client.GetContractWasmHash(context.Background(), hexID)
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(wasmHash), version)
}

func mustDecodeStrkey(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteContract, s)
	require.NoError(t, err)
	return raw
}

// TestGetContractWasmHash_RequestsTheInstanceLedgerKey checks the request asks
// for the contract instance entry, since that is the only entry carrying the
// executable hash.
func TestGetContractWasmHash_RequestsTheInstanceLedgerKey(t *testing.T) {
	var request map[string]any
	var params map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&request)
		params, _ = request["params"].(map[string]any)
		_, _ = w.Write([]byte(entriesBody(t, wasmInstanceEntry(t, testContractID, bytes32(0x01)))))
	}))
	defer srv.Close()

	_, err := soroban.NewLedgerClient(srv.URL).GetContractWasmHash(context.Background(), testContractID)
	require.NoError(t, err)

	assert.Equal(t, "getLedgerEntries", request["method"])

	keys, ok := params["keys"].([]any)
	require.True(t, ok, "getLedgerEntries takes a keys array, got %#v", params["keys"])
	require.Len(t, keys, 1)

	encoded, ok := keys[0].(string)
	require.True(t, ok, "ledger keys are sent base64 encoded, got %T", keys[0])

	raw, err := decodeBase64(encoded)
	require.NoError(t, err)

	var key xdr.LedgerKey
	_, err = xdr.Unmarshal(bytes.NewReader(raw), &key)
	require.NoError(t, err)

	require.NotNil(t, key.ContractData)
	assert.Equal(t, xdr.LedgerEntryTypeContractData, key.Type)
	assert.Equal(t, xdr.ContractDataDurabilityPersistent, key.ContractData.Durability)
	assert.Equal(t, xdr.ScValTypeScvLedgerKeyContractInstance, key.ContractData.Key.Type)
	assert.Equal(t, hex.EncodeToString(mustDecodeStrkey(t, testContractID)),
		hex.EncodeToString(key.ContractData.Contract.ContractId[:]))
}

// TestGetContractWasmHash_MissingEntry checks an absent contract is reported as
// not found rather than as an empty version.
func TestGetContractWasmHash_MissingEntry(t *testing.T) {
	client := serveEntries(t, `{"jsonrpc":"2.0","id":1,"result":{"entries":[],"latestLedger":101}}`)

	_, err := client.GetContractWasmHash(context.Background(), testContractID)
	assert.ErrorIs(t, err, soroban.ErrContractNotFound)
}

// TestGetContractWasmHash_EntryWithoutInstance checks a contract data entry that
// is not an instance is reported as not found.
func TestGetContractWasmHash_EntryWithoutInstance(t *testing.T) {
	notAnInstance := true
	entry := xdr.LedgerEntryData{
		Type: xdr.LedgerEntryTypeContractData,
		ContractData: &xdr.ContractDataEntry{
			Contract:   contractAddress(testContractID),
			Durability: xdr.ContractDataDurabilityTemporary,
			Key: xdr.ScVal{
				Type:     xdr.ScValTypeScvLedgerKeyContractInstance,
				Instance: &xdr.ScContractInstance{},
			},
			Val: xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &notAnInstance},
		},
	}
	encoded, err := xdr.MarshalBase64(entry)
	require.NoError(t, err)

	client := serveEntries(t, entriesBody(t, encoded))
	_, err = client.GetContractWasmHash(context.Background(), testContractID)
	assert.ErrorIs(t, err, soroban.ErrContractNotFound)
}

func TestGetContractWasmHash_RPCError(t *testing.T) {
	client := serveEntries(t, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"bad request"}}`)

	_, err := client.GetContractWasmHash(context.Background(), testContractID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad request")
}

func TestGetContractWasmHash_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream unavailable"))
	}))
	defer srv.Close()

	_, err := soroban.NewLedgerClient(srv.URL).GetContractWasmHash(context.Background(), testContractID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestGetContractWasmHash_InvalidContractID(t *testing.T) {
	client := serveEntries(t, entriesBody(t, wasmInstanceEntry(t, testContractID, bytes32(0x01))))

	for _, id := range []string{"", "   ", "not-a-contract", "Ctooshort", strings.Repeat("z", 56)} {
		_, err := client.GetContractWasmHash(context.Background(), id)
		assert.Error(t, err, "contract id %q must be rejected", id)
	}
}

// TestGetContractWasmHash_UnreachableNode checks a transport failure surfaces as
// an error rather than an empty version.
func TestGetContractWasmHash_UnreachableNode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	_, err := soroban.NewLedgerClient(url).GetContractWasmHash(context.Background(), testContractID)
	require.Error(t, err)
	assert.False(t, errors.Is(err, soroban.ErrContractNotFound))
}
