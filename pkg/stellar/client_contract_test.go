package stellar_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/stellar"
	"github.com/moistello/backend/pkg/stellar/stellartest"
)

const (
	testAccount   = "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"
	testRecipient = "GCEZWKCA5VLDNRLN3RPRJMRZOX3Z6G5CHCGVEF1MSIFWIO6CUDR5"
	passphrase    = "Test SDF Network ; September 2015"
)

func newClient(n *stellartest.Network) *stellar.Client {
	return stellar.NewClient(n.HorizonURL(), n.RPCURL(), passphrase)
}

func TestClient_GetAccount_ParsesHorizonRecord(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.SetAccount(testAccount, 4242,
		stellartest.Balance{Balance: "100.5000000", AssetType: "native"},
		stellartest.Balance{Balance: "25.0000000", AssetType: "credit_alphanum4", AssetCode: "USDC", AssetIssuer: testRecipient},
	)

	acc, err := newClient(n).GetAccount(context.Background(), testAccount)
	require.NoError(t, err)
	assert.Equal(t, testAccount, acc.ID)
	assert.Equal(t, "4242", acc.Sequence)
	require.Len(t, acc.Balances, 2)
	assert.Equal(t, "100.5000000", acc.Balances[0].Balance)
	assert.Equal(t, "native", acc.Balances[0].AssetType)
	assert.Equal(t, "USDC", acc.Balances[1].AssetCode)
	assert.Equal(t, testRecipient, acc.Balances[1].AssetIssuer)

	reqs := n.HorizonRequests()
	require.Len(t, reqs, 1)
	assert.Equal(t, "GET", reqs[0].Method)
	assert.Equal(t, "/accounts/"+testAccount, reqs[0].Path)
}

func TestClient_GetAccount_NotFound(t *testing.T) {
	n := stellartest.New()
	defer n.Close()

	acc, err := newClient(n).GetAccount(context.Background(), testAccount)
	assert.Nil(t, acc)
	require.EqualError(t, err, "account not found")
}

func TestClient_GetAccount_ServerErrorIncludesBody(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.QueueHorizon(stellartest.HorizonResponse{HTTPStatus: 503, Body: `{"title":"Service Unavailable"}`})

	_, err := newClient(n).GetAccount(context.Background(), testAccount)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "horizon error 503")
	assert.Contains(t, err.Error(), "Service Unavailable")
}

func TestClient_GetAccount_MalformedJSON(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.QueueHorizon(stellartest.HorizonResponse{HTTPStatus: 200, Body: `{"id": "x", "sequence": `})

	_, err := newClient(n).GetAccount(context.Background(), testAccount)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding horizon response")
}

func TestClient_CircuitBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	failures := stellar.DefaultCircuitBreakerConfig().FailureThreshold
	for i := 0; i < failures; i++ {
		n.QueueHorizon(stellartest.HorizonResponse{HTTPStatus: 500, Body: "boom"})
	}
	n.SetAccount(testAccount, 1)

	client := newClient(n)
	for i := 0; i < failures; i++ {
		_, err := client.GetAccount(context.Background(), testAccount)
		require.Error(t, err)
	}
	_, err := client.GetAccount(context.Background(), testAccount)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OPEN", "breaker rejects fast once the threshold is reached")
	assert.Len(t, n.HorizonRequests(), failures, "the rejected call never reached Horizon")
}

func TestClient_VerifyTransaction_MatchesSenderAndAmount(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.SetTransaction(stellartest.HorizonTransaction{
		Hash:       "tx-1",
		Successful: true,
		Operations: []map[string]any{
			{"type": "change_trust", "from": testAccount},
			{"type": "payment", "from": testAccount, "to": testRecipient, "amount": "100.0000000"},
		},
	})
	client := newClient(n)
	ctx := context.Background()

	ok, err := client.VerifyTransaction(ctx, "tx-1", testAccount, "100.0000000")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = client.VerifyTransaction(ctx, "tx-1", testAccount, "99.0000000")
	require.NoError(t, err)
	assert.False(t, ok, "amount mismatch is a clean negative, not an error")

	ok, err = client.VerifyTransaction(ctx, "tx-1", testRecipient, "")
	require.NoError(t, err)
	assert.False(t, ok, "only payment operations count, and the sender must match")

	ok, err = client.VerifyPayment(ctx, "tx-1", testRecipient, "100.0000000")
	require.NoError(t, err)
	assert.True(t, ok)

	reqs := n.HorizonRequests()
	require.GreaterOrEqual(t, len(reqs), 2)
	assert.Equal(t, "/transactions/tx-1", reqs[0].Path)
	assert.Equal(t, "/transactions/tx-1/operations?limit=200", reqs[1].Path)
}

func TestClient_VerifyTransaction_ErrorPaths(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.SetTransaction(stellartest.HorizonTransaction{Hash: "failed-tx", Successful: false})
	client := newClient(n)
	ctx := context.Background()

	_, err := client.VerifyTransaction(ctx, "failed-tx", testAccount, "1.0000000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was not successful on-chain")

	_, err = client.VerifyPayment(ctx, "missing-tx", testRecipient, "1.0000000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "horizon error 404")
}

func TestAccountManager_NextSequence_FetchesOnceThenIncrements(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	n.SetAccount(testAccount, 100)
	mgr := stellar.NewAccountManager(newClient(n), testAccount)
	ctx := context.Background()

	first, err := mgr.NextSequence(ctx)
	require.NoError(t, err)
	second, err := mgr.NextSequence(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(100), first)
	assert.Equal(t, int64(101), second)
	assert.Len(t, n.HorizonRequests(), 1, "the cached sequence is reused within the drift window")

	n.SetAccount(testAccount, 500)
	mgr.Reset()
	third, err := mgr.NextSequence(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(500), third, "Reset reloads the sequence from Horizon")
	assert.Len(t, n.HorizonRequests(), 2)
}

func TestAccountManager_NextSequence_PropagatesHorizonFailure(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	mgr := stellar.NewAccountManager(newClient(n), testAccount)

	_, err := mgr.NextSequence(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetching sequence")
}

func TestHealthChecker_ReportsHorizonAndMasterAccount(t *testing.T) {
	n := stellartest.New()
	defer n.Close()
	// CheckHorizon probes the well-known base reserve account; the master
	// account under test is a different address.
	n.SetAccount(testAccount, 1, stellartest.Balance{Balance: "1.0000000", AssetType: "native"})
	n.SetAccount(testRecipient, 1, stellartest.Balance{Balance: "42.0000000", AssetType: "native"})

	results := stellar.NewHealthChecker(newClient(n)).CheckAll(context.Background(), testRecipient)
	require.Len(t, results, 2)
	assert.Equal(t, "horizon", results[0].Component)
	assert.True(t, results[0].Healthy)
	assert.Equal(t, "master_account", results[1].Component)
	assert.True(t, results[1].Healthy)
	assert.True(t, strings.HasPrefix(results[1].Message, "balance: 42.0000000 XLM"), results[1].Message)
	assert.WithinDuration(t, time.Now(), results[1].LastCheck, 5*time.Second)
}

func TestHealthChecker_UnreachableHorizon(t *testing.T) {
	n := stellartest.New()
	n.Close()

	status := stellar.NewHealthChecker(newClient(n)).CheckHorizon(context.Background())
	assert.False(t, status.Healthy)
	assert.Contains(t, status.Message, "unreachable")
}
