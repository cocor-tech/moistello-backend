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

func TestContractDeployer(t *testing.T) {
	tmpDir := t.TempDir()
	wasmPath := filepath.Join(tmpDir, "test.wasm")
	require.NoError(t, os.WriteFile(wasmPath, []byte("\x00asm\x01\x00\x00\x00"), 0600))

	t.Run("DeployContract and InstallWASM invalid path errors", func(t *testing.T) {
		f := newInvokerFixture(t)
		horizon := stellar.NewClient(f.net.HorizonURL(), f.net.RPCURL(), "Test SDF Network ; September 2015")
		accountMgr := stellar.NewAccountManager(horizon, f.address)
		deployer := soroban.NewContractDeployer(fastClient(f.net), f.signer, accountMgr)

		// Empty path
		_, err := deployer.DeployContract(context.Background(), "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "validating wasm path: path is empty")

		_, err = deployer.InstallWASM(context.Background(), "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "validating wasm path: path is empty")

		// Non-existent file
		_, err = deployer.DeployContract(context.Background(), "nonexistent.wasm")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "validating wasm path:")

		// Directory path
		_, err = deployer.DeployContract(context.Background(), tmpDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "path is not a regular file")
	})

	t.Run("InstallWASM sequence error", func(t *testing.T) {
		f := newInvokerFixture(t)
		f.net.QueueHorizon(stellartest.HorizonResponse{HTTPStatus: 500, Body: "horizon error"})

		horizon := stellar.NewClient(f.net.HorizonURL(), f.net.RPCURL(), "Test SDF Network ; September 2015")
		accountMgr := stellar.NewAccountManager(horizon, f.address)
		deployer := soroban.NewContractDeployer(fastClient(f.net), f.signer, accountMgr)

		_, err := deployer.InstallWASM(context.Background(), wasmPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getting sequence for upload")
	})

	t.Run("InstallWASM simulation error", func(t *testing.T) {
		f := newInvokerFixture(t)
		f.net.QueueRPC("simulateTransaction", stellartest.RPCResponse{HTTPStatus: 500, RawBody: "rpc error"})

		horizon := stellar.NewClient(f.net.HorizonURL(), f.net.RPCURL(), "Test SDF Network ; September 2015")
		accountMgr := stellar.NewAccountManager(horizon, f.address)
		deployer := soroban.NewContractDeployer(fastClient(f.net), f.signer, accountMgr)

		_, err := deployer.InstallWASM(context.Background(), wasmPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "simulating upload")
	})

	t.Run("InstallWASM simulation returns contract error", func(t *testing.T) {
		f := newInvokerFixture(t)
		f.net.SetSimulateResult(map[string]any{"error": "WASM size limit exceeded", "events": []string{}, "cost": map[string]any{}})

		horizon := stellar.NewClient(f.net.HorizonURL(), f.net.RPCURL(), "Test SDF Network ; September 2015")
		accountMgr := stellar.NewAccountManager(horizon, f.address)
		deployer := soroban.NewContractDeployer(fastClient(f.net), f.signer, accountMgr)

		_, err := deployer.InstallWASM(context.Background(), wasmPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "upload simulation error: WASM size limit exceeded")
	})

	t.Run("InstallWASM reverted tx", func(t *testing.T) {
		f := newInvokerFixture(t)
		f.net.QueueRPC("getTransaction", stellartest.RPCResponse{Result: map[string]any{"status": "FAILED", "resultXdr": "AAAAREVERT", "ledger": 10}})

		horizon := stellar.NewClient(f.net.HorizonURL(), f.net.RPCURL(), "Test SDF Network ; September 2015")
		accountMgr := stellar.NewAccountManager(horizon, f.address)
		deployer := soroban.NewContractDeployer(fastClient(f.net), f.signer, accountMgr)

		_, err := deployer.InstallWASM(context.Background(), wasmPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "upload tx reverted: AAAAREVERT")
	})

	t.Run("InstallWASM send tx failure", func(t *testing.T) {
		f := newInvokerFixture(t)
		f.net.QueueRPC("sendTransaction", stellartest.RPCResponse{HTTPStatus: 400, RawBody: "bad request"})

		horizon := stellar.NewClient(f.net.HorizonURL(), f.net.RPCURL(), "Test SDF Network ; September 2015")
		accountMgr := stellar.NewAccountManager(horizon, f.address)
		deployer := soroban.NewContractDeployer(fastClient(f.net), f.signer, accountMgr)

		_, err := deployer.InstallWASM(context.Background(), wasmPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "upload tx failed")
	})

	t.Run("DeployContract full happy path", func(t *testing.T) {
		f := newInvokerFixture(t)
		queueSuccessTx(f.net) // upload
		queueSuccessTx(f.net) // create

		horizon := stellar.NewClient(f.net.HorizonURL(), f.net.RPCURL(), "Test SDF Network ; September 2015")
		accountMgr := stellar.NewAccountManager(horizon, f.address)
		deployer := soroban.NewContractDeployer(fastClient(f.net), f.signer, accountMgr)

		contractID, err := deployer.DeployContract(context.Background(), wasmPath, "admin-address", uint32(10))
		require.NoError(t, err)
		assert.Equal(t, "", contractID)
	})

	t.Run("DeployContract create contract simulation error", func(t *testing.T) {
		f := newInvokerFixture(t)
		queueSuccessTx(f.net) // upload succeeds

		// simulate create contract fails
		f.net.QueueRPC("simulateTransaction", stellartest.RPCResponse{HTTPStatus: 500, RawBody: "simulate error"})

		horizon := stellar.NewClient(f.net.HorizonURL(), f.net.RPCURL(), "Test SDF Network ; September 2015")
		accountMgr := stellar.NewAccountManager(horizon, f.address)
		deployer := soroban.NewContractDeployer(fastClient(f.net), f.signer, accountMgr)

		_, err := deployer.DeployContract(context.Background(), wasmPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "simulating create")
	})

	t.Run("DeployContract create contract simulation returns error", func(t *testing.T) {
		f := newInvokerFixture(t)
		queueSuccessTx(f.net) // upload succeeds

		// simulate returns error object on second call
		f.net.SetSimulateResult(map[string]any{"error": "Init failed", "events": []string{}, "cost": map[string]any{}})

		horizon := stellar.NewClient(f.net.HorizonURL(), f.net.RPCURL(), "Test SDF Network ; September 2015")
		accountMgr := stellar.NewAccountManager(horizon, f.address)
		deployer := soroban.NewContractDeployer(fastClient(f.net), f.signer, accountMgr)

		_, err := deployer.DeployContract(context.Background(), wasmPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "upload simulation error: Init failed")
	})

	t.Run("DeployContract create contract reverted", func(t *testing.T) {
		f := newInvokerFixture(t)
		queueSuccessTx(f.net) // upload succeeds
		f.net.QueueRPC("getTransaction", stellartest.RPCResponse{Result: map[string]any{"status": "FAILED", "resultXdr": "CREATE_FAIL", "ledger": 10}})

		horizon := stellar.NewClient(f.net.HorizonURL(), f.net.RPCURL(), "Test SDF Network ; September 2015")
		accountMgr := stellar.NewAccountManager(horizon, f.address)
		deployer := soroban.NewContractDeployer(fastClient(f.net), f.signer, accountMgr)

		_, err := deployer.DeployContract(context.Background(), wasmPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "create tx reverted: CREATE_FAIL")
	})

	t.Run("DeployContract create contract send tx error", func(t *testing.T) {
		f := newInvokerFixture(t)
		queueSuccessTx(f.net) // upload succeeds
		f.net.QueueRPC("sendTransaction", stellartest.RPCResponse{HTTPStatus: 400, RawBody: "bad request"})

		horizon := stellar.NewClient(f.net.HorizonURL(), f.net.RPCURL(), "Test SDF Network ; September 2015")
		accountMgr := stellar.NewAccountManager(horizon, f.address)
		deployer := soroban.NewContractDeployer(fastClient(f.net), f.signer, accountMgr)

		_, err := deployer.DeployContract(context.Background(), wasmPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "create tx failed")
	})
}
