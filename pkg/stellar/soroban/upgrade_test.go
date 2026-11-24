package soroban_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/stellar"
	"github.com/moistello/backend/pkg/stellar/soroban"
	"github.com/moistello/backend/pkg/stellar/stellartest"
)

func TestUpgradeClient(t *testing.T) {
	t.Run("GetImplementation success", func(t *testing.T) {
		f := newInvokerFixture(t)
		queueSuccessTx(f.net)

		client := soroban.NewUpgradeClient(nil, f.invoker)
		impl, err := client.GetImplementation(context.Background(), "proxy-id")
		require.NoError(t, err)
		assert.Equal(t, "", impl)
	})

	t.Run("GetImplementation error", func(t *testing.T) {
		f := newInvokerFixture(t)
		f.net.QueueRPC("simulateTransaction", stellartest.RPCResponse{HTTPStatus: 500, RawBody: "error"})

		client := soroban.NewUpgradeClient(nil, f.invoker)
		_, err := client.GetImplementation(context.Background(), "proxy-id")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading implementation:")
	})

	t.Run("UpgradeContract deployer failure returns error", func(t *testing.T) {
		f := newInvokerFixture(t)
		deployer := soroban.NewContractDeployer(nil, f.signer, stellar.NewAccountManager(nil, f.address))
		client := soroban.NewUpgradeClient(deployer, f.invoker)

		_, _, err := client.UpgradeContract(context.Background(), "non-existent-wasm-file.wasm", "proxy-123")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deploying new implementation:")
	})

	t.Run("UpgradeContract full lifecycle success and proxy update failure", func(t *testing.T) {
		f := newInvokerFixture(t)
		horizon := stellar.NewClient(f.net.HorizonURL(), f.net.RPCURL(), "Test SDF Network ; September 2015")
		accountMgr := stellar.NewAccountManager(horizon, f.address)

		fastCl := fastClient(f.net)
		deployer := soroban.NewContractDeployer(fastCl, f.signer, accountMgr)
		client := soroban.NewUpgradeClient(deployer, f.invoker)

		// Create a temporary valid WASM file
		tmpDir := t.TempDir()
		wasmPath := filepath.Join(tmpDir, "contract.wasm")
		require.NoError(t, os.WriteFile(wasmPath, []byte("\x00asm\x01\x00\x00\x00"), 0600))

		// 1. Success path: DeployContract needs getTransaction twice (upload + create) + set_implementation once
		queueSuccessTx(f.net) // upload
		queueSuccessTx(f.net) // create
		queueSuccessTx(f.net) // set_implementation

		newImpl, upHash, err := client.UpgradeContract(context.Background(), wasmPath, "proxy-123")
		require.NoError(t, err)
		assert.Equal(t, "", newImpl) // extractContractIDFromResult returns ""
		assert.NotEmpty(t, upHash)

		// 2. Failure updating proxy
		queueSuccessTx(f.net) // upload
		queueSuccessTx(f.net) // create
		f.net.QueueRPC("simulateTransaction", stellartest.RPCResponse{HTTPStatus: 500, RawBody: "simulate error"})

		_, _, err = client.UpgradeContract(context.Background(), wasmPath, "proxy-123")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "updating proxy:")
	})
}
