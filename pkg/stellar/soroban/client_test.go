package soroban_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/stellar"
	"github.com/moistello/backend/pkg/stellar/soroban"
	"github.com/moistello/backend/pkg/stellar/stellartest"
)

func TestSorobanClient_BasicMethods(t *testing.T) {
	n := stellartest.New()
	defer n.Close()

	n.SetAccount("GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H", 500)

	client := soroban.NewClient(n.RPCURL())
	assert.Equal(t, n.RPCURL(), client.RPCURL())

	subCfg := soroban.DefaultSubmitConfig()
	assert.Equal(t, 5, subCfg.MaxRetries)
	assert.Equal(t, 2*time.Second, subCfg.RetryDelay)

	pollCfg := soroban.DefaultPollConfig()
	assert.Equal(t, 60, pollCfg.MaxAttempts)
	assert.Equal(t, 1*time.Second, pollCfg.Interval)

	t.Run("GetAccount success", func(t *testing.T) {
		// NewClient internally creates a stellar.NewClient("", c.rpcURL, "") where rpcURL is passed as horizonURL
		// To test GetAccount, point to the Horizon endpoint:
		hClient := soroban.NewClient(n.HorizonURL())
		acc, err := hClient.GetAccount(context.Background(), "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H")
		require.NoError(t, err)
		assert.Equal(t, "500", acc.Sequence)

		// Error case
		_, err = hClient.GetAccount(context.Background(), "GUNKNOWN")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "soroban: get account")
	})

	t.Run("GetTransaction success and error", func(t *testing.T) {
		fastCl := fastClient(n)
		n.SetTransactionStatuses("test-hash-123", "SUCCESS")

		res, err := fastCl.GetTransaction(context.Background(), "test-hash-123")
		require.NoError(t, err)
		assert.NotNil(t, res)

		// Bad RPC response
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("rpc fail"))
		}))
		defer srv.Close()

		badCl := soroban.NewClient(srv.URL).WithBackoff(stellar.BackoffConfig{MaxRetries: 0})
		_, err = badCl.GetTransaction(context.Background(), "hash")
		require.Error(t, err)
	})

	t.Run("SimulateTransaction success", func(t *testing.T) {
		fastCl := fastClient(n)
		tx := stellar.NewTransactionBuilder("GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H").Build(501)

		res, err := fastCl.SimulateTransaction(context.Background(), tx)
		require.NoError(t, err)
		assert.NotNil(t, res)
	})

	t.Run("rpcCall invalid json returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{invalid-json-content"))
		}))
		defer srv.Close()

		badCl := soroban.NewClient(srv.URL).WithBackoff(stellar.BackoffConfig{MaxRetries: 0})
		_, err := badCl.GetTransaction(context.Background(), "hash")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decoding rpc response")
	})

	t.Run("SendTransaction context cancelled during retry delay", func(t *testing.T) {
		n.QueueRPC("sendTransaction", stellartest.RPCResponse{HTTPStatus: 429, RawBody: "rate limited"})

		ctx, cancel := context.WithCancel(context.Background())
		client := soroban.NewClient(n.RPCURL()).WithTimings(
			soroban.SubmitConfig{MaxRetries: 3, RetryDelay: 500 * time.Millisecond},
			soroban.PollConfig{MaxAttempts: 5, Interval: time.Millisecond},
		)

		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		_, err := client.SendTransaction(ctx, "signed-env")
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("SendTransaction context cancelled during poll", func(t *testing.T) {
		hash := stellartest.TxHash("signed-env-poll-cancel")
		n.SetTransactionStatuses(hash, "NOT_FOUND", "NOT_FOUND", "NOT_FOUND")

		ctx, cancel := context.WithCancel(context.Background())
		client := soroban.NewClient(n.RPCURL()).WithTimings(
			soroban.SubmitConfig{MaxRetries: 0, RetryDelay: time.Millisecond},
			soroban.PollConfig{MaxAttempts: 50, Interval: 100 * time.Millisecond},
		)

		go func() {
			time.Sleep(30 * time.Millisecond)
			cancel()
		}()

		_, err := client.SendTransaction(ctx, "signed-env-poll-cancel")
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})
}
