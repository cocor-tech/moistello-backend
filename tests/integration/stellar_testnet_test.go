package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	masterPublicKey = "GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC"
	horizonURL      = "https://horizon-testnet.stellar.org"
	testnetPassphrase = "Test SDF Network ; September 2015"
)

func masterSecretKey() string {
	if sk := os.Getenv("MOISTELLO_STELLAR_MASTER_SECRET_KEY"); sk != "" {
		return sk
	}
	return os.Getenv("STELLAR_MASTER_SECRET_KEY")
}

type horizonAccount struct {
	ID        string `json:"id"`
	Sequence  string `json:"sequence"`
	Balances  []struct {
		Balance   string `json:"balance"`
		AssetType string `json:"asset_type"`
		AssetCode string `json:"asset_code,omitempty"`
	} `json:"balances"`
	SubentryCount int `json:"subentry_count"`
}

func fetch(url string, target any) error {
	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("horizon %d: %s", resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

// ── Test 1: Verify Account Exists on Testnet ──
func TestStellar_VerifyAccountExists(t *testing.T) {
	url := fmt.Sprintf("%s/accounts/%s", horizonURL, masterPublicKey)
	var acc horizonAccount
	err := fetch(url, &acc)
	require.NoError(t, err)
	assert.Equal(t, masterPublicKey, acc.ID)
	assert.NotEmpty(t, acc.Sequence)

	var xlmBalance string
	for _, b := range acc.Balances {
		if b.AssetType == "native" {
			xlmBalance = b.Balance
		}
	}
	assert.NotEmpty(t, xlmBalance)
	t.Logf("Account: %s | XLM Balance: %s | Sequence: %s", acc.ID, xlmBalance, acc.Sequence)
}

// ── Test 2: Verify Account Has Enough XLM ──
func TestStellar_HasMinimumBalance(t *testing.T) {
	url := fmt.Sprintf("%s/accounts/%s", horizonURL, masterPublicKey)
	var acc horizonAccount
	err := fetch(url, &acc)
	require.NoError(t, err)

	for _, b := range acc.Balances {
		if b.AssetType == "native" {
			balance, _ := parseFloat(b.Balance)
			assert.True(t, balance >= 1.0, "account must have at least 1 XLM (has %s)", b.Balance)
			t.Logf("XLM Balance: %s", b.Balance)
		}
	}
}

// ── Test 3: Verify Transaction History Accessible ──
func TestStellar_TransactionHistory(t *testing.T) {
	url := fmt.Sprintf("%s/accounts/%s/transactions?limit=5&order=desc", horizonURL, masterPublicKey)
	type txnResponse struct {
		Embedded struct {
			Records []map[string]any `json:"records"`
		} `json:"_embedded"`
	}
	var resp txnResponse
	err := fetch(url, &resp)
	require.NoError(t, err)
	t.Logf("Transaction count (last 5): %d", len(resp.Embedded.Records))
}

// ── Test 4: Verify Network Passphrase ──
func TestStellar_NetworkPassphrase(t *testing.T) {
	url := fmt.Sprintf("%s/ledgers?limit=1", horizonURL)
	type ledgerResp struct {
		Embedded struct {
			Records []map[string]any `json:"records"`
		} `json:"_embedded"`
	}
	var resp ledgerResp
	err := fetch(url, &resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Embedded.Records, "testnet should have ledgers")
	t.Logf("Latest ledger number: %v", resp.Embedded.Records[0]["sequence"])
}

// ── Test 5: Verify Account Can Receive Payments ──
func TestStellar_AccountActivated(t *testing.T) {
	url := fmt.Sprintf("%s/accounts/%s", horizonURL, masterPublicKey)
	var acc horizonAccount
	err := fetch(url, &acc)
	require.NoError(t, err)

	// An activated account has a non-zero sequence and native balance
	seq := acc.Sequence
	assert.NotEmpty(t, seq)
	assert.NotEqual(t, "0", seq)

	hasNative := false
	for _, b := range acc.Balances {
		if b.AssetType == "native" {
			hasNative = true
			break
		}
	}
	assert.True(t, hasNative, "account must have native XLM balance")
}

// ── Test 6: Horizon Health Check ──
func TestStellar_HorizonHealth(t *testing.T) {
	url := fmt.Sprintf("%s", horizonURL)
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "horizon API should respond 200")
	t.Logf("Horizon: %d OK", resp.StatusCode)
}

// ── Test 7: Account Has No Unexpected Signers ──
func TestStellar_SignerCount(t *testing.T) {
	url := fmt.Sprintf("%s/accounts/%s", horizonURL, masterPublicKey)
	type signerResp struct {
		Signers []map[string]any `json:"signers"`
	}
	var acc signerResp
	err := fetch(url, &acc)
	require.NoError(t, err)
	assert.Equal(t, 1, len(acc.Signers), "master account should have exactly 1 signer")
	t.Logf("Signer count: %d (weight: %v)", len(acc.Signers), acc.Signers[0]["weight"])
}

// ── Test 8: Verify Keypair Validity ──
func TestStellar_KeypairFormat(t *testing.T) {
	// Stellar public keys start with G and are 56 chars base32
	assert.Len(t, masterPublicKey, 56)
	assert.Equal(t, "G", string(masterPublicKey[0]))

	// Stellar secret keys start with S and are 56 chars base32
	assert.Len(t, masterSecretKey(), 56)
	assert.Equal(t, "S", string(masterSecretKey()[0]))

	t.Logf("Public key format: ✓ (G...56 chars)")
	t.Logf("Secret key format: ✓ (S...56 chars)")
}

// ── Test 9: Config Values Loaded ──
func TestStellar_ConfigValuesNonEmpty(t *testing.T) {
	assert.NotEmpty(t, masterPublicKey, "master public key must be set")
	assert.NotEmpty(t, masterSecretKey(), "master secret key must be set via env var")
	assert.NotEmpty(t, horizonURL, "horizon URL must be set")
	assert.NotEmpty(t, testnetPassphrase, "network passphrase must be set")
}

// ── Test 10: Network Info (Root Endpoint) ──
func TestStellar_RootEndpoint(t *testing.T) {
	url := fmt.Sprintf("%s/", horizonURL)
	type rootResp struct {
		NetworkPassphrase string `json:"network_passphrase"`
		HorizonVersion    string `json:"horizon_version"`
		CoreVersion       string `json:"core_version"`
	}
	var root rootResp
	err := fetch(url, &root)
	require.NoError(t, err)
	assert.Equal(t, testnetPassphrase, root.NetworkPassphrase)
	t.Logf("Horizon: %s | Core: %s | Network: %s", root.HorizonVersion, root.CoreVersion, root.NetworkPassphrase)
}

// helper
func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

var _ = context.Background // unused but explicit
