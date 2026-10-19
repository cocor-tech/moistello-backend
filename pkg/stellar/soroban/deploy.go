package soroban

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/moistello/backend/pkg/stellar"
)

// ContractDeployer handles WASM upload and contract instantiation.
type ContractDeployer struct {
	client     *Client
	signer     *stellar.Signer
	accountMgr *stellar.AccountManager
}

func NewContractDeployer(client *Client, signer *stellar.Signer, accountMgr *stellar.AccountManager) *ContractDeployer {
	return &ContractDeployer{
		client:     client,
		signer:     signer,
		accountMgr: accountMgr,
	}
}

// DeployContract uploads WASM and creates a contract instance.
// Steps:
//  1. Read WASM file from disk
//  2. Upload WASM to Soroban RPC (install)
//  3. Create (instantiate) the contract with init args
func (d *ContractDeployer) DeployContract(ctx context.Context, wasmPath string, initArgs ...interface{}) (string, error) {
	wasmPath, err := validatedWASMPath(wasmPath)
	if err != nil {
		return "", fmt.Errorf("validating wasm path: %w", err)
	}
	wasmBytes, err := os.ReadFile(wasmPath) // #nosec G304 -- validated regular WASM path
	if err != nil {
		return "", fmt.Errorf("reading wasm file %s: %w", wasmPath, err)
	}

	wasmHash, err := d.uploadWASM(ctx, wasmBytes)
	if err != nil {
		return "", fmt.Errorf("uploading wasm: %w", err)
	}

	contractID, err := d.createContract(ctx, wasmHash, initArgs...)
	if err != nil {
		return "", fmt.Errorf("creating contract instance: %w", err)
	}

	return contractID, nil
}

// InstallWASM uploads WASM bytes to the network and returns the hash.
func (d *ContractDeployer) InstallWASM(ctx context.Context, wasmPath string) (string, error) {
	wasmPath, err := validatedWASMPath(wasmPath)
	if err != nil {
		return "", fmt.Errorf("validating wasm path: %w", err)
	}
	wasmBytes, err := os.ReadFile(wasmPath) // #nosec G304 -- validated regular WASM path
	if err != nil {
		return "", fmt.Errorf("reading wasm file %s: %w", wasmPath, err)
	}
	return d.uploadWASM(ctx, wasmBytes)
}

func validatedWASMPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("path is not a regular file")
	}
	return abs, nil
}

// uploadWASM submits a HostFunction install operation on behalf of the deployer.
func (d *ContractDeployer) uploadWASM(ctx context.Context, wasmBytes []byte) (string, error) {
	// Build a transaction with an upload WASM host function operation
	builder := stellar.NewTransactionBuilder(d.accountMgr.PublicKey())

	uploadOp := UploadWasmOp{
		Wasm: base64.StdEncoding.EncodeToString(wasmBytes),
	}
	builder.AddOperation(uploadOp)

	seq, err := d.accountMgr.NextSequence(ctx)
	if err != nil {
		return "", fmt.Errorf("getting sequence for upload: %w", err)
	}
	tx := builder.Build(seq)

	// Simulate to get cost estimates
	simResult, err := d.client.SimulateTransaction(ctx, tx)
	if err != nil {
		return "", fmt.Errorf("simulating upload: %w", err)
	}
	if simResult.Error != nil {
		return "", fmt.Errorf("upload simulation error: %s", *simResult.Error)
	}

	invoker := NewContractInvoker(d.client, d.signer, d.accountMgr, "")
	tx = invoker.applyResources(tx, simResult)

	signedEnvelope, err := invoker.signTransaction(tx, "Test SDF Network ; September 2015")
	if err != nil {
		return "", fmt.Errorf("signing upload tx: %w", err)
	}

	result, err := d.client.SendTransaction(ctx, signedEnvelope)
	if err != nil {
		if result != nil {
			return "", fmt.Errorf("upload tx failed (%s): %w", result.Hash, err)
		}
		return "", fmt.Errorf("upload tx failed: %w", err)
	}
	if !result.Successful {
		return "", fmt.Errorf("upload tx reverted: %s", result.ResultXDR)
	}

	// Retrieve the WASM hash from the transaction result events
	wasmHash, err := d.extractWasmHashFromResult(result.ResultXDR)
	if err != nil {
		return "", fmt.Errorf("extracting wasm hash: %w", err)
	}
	return wasmHash, nil
}

// createContract creates a contract instance from an installed WASM hash.
func (d *ContractDeployer) createContract(ctx context.Context, wasmHash string, initArgs ...interface{}) (string, error) {
	sorobanArgs := make([]stellar.SorobanArg, len(initArgs)+1)
	sorobanArgs[0] = stellar.SorobanArg{Type: "bytes", Value: wasmHash}
	for i, arg := range initArgs {
		sorobanArgs[i+1] = toSorobanArg(arg)
	}

	builder := stellar.NewTransactionBuilder(d.accountMgr.PublicKey())
	createOp := CreateContractOp{
		WasmHash: wasmHash,
		InitArgs: sorobanArgs[1:],
		Salt:     d.accountMgr.PublicKey(),
	}
	builder.AddOperation(createOp)

	seq, err := d.accountMgr.NextSequence(ctx)
	if err != nil {
		return "", fmt.Errorf("getting sequence for create: %w", err)
	}
	tx := builder.Build(seq)

	simResult, err := d.client.SimulateTransaction(ctx, tx)
	if err != nil {
		return "", fmt.Errorf("simulating create: %w", err)
	}
	if simResult.Error != nil {
		return "", fmt.Errorf("create simulation error: %s", *simResult.Error)
	}

	invoker := NewContractInvoker(d.client, d.signer, d.accountMgr, "")
	tx = invoker.applyResources(tx, simResult)

	signedEnvelope, err := invoker.signTransaction(tx, "Test SDF Network ; September 2015")
	if err != nil {
		return "", fmt.Errorf("signing create tx: %w", err)
	}

	result, err := d.client.SendTransaction(ctx, signedEnvelope)
	if err != nil {
		if result != nil {
			return "", fmt.Errorf("create tx failed (%s): %w", result.Hash, err)
		}
		return "", fmt.Errorf("create tx failed: %w", err)
	}
	if !result.Successful {
		return "", fmt.Errorf("create tx reverted: %s", result.ResultXDR)
	}

	contractID, err := d.extractContractIDFromResult(result.ResultXDR)
	if err != nil {
		return "", fmt.Errorf("extracting contract id: %w", err)
	}
	return contractID, nil
}

// extractWasmHashFromResult parses the WASM hash from transaction result metadata.
// In production this would decode the XDR result and extract the install footprint.
func (d *ContractDeployer) extractWasmHashFromResult(resultXDR string) (string, error) {
	_ = resultXDR
	return "", nil
}

// extractContractIDFromResult parses the contract ID from the create result metadata.
// In production this would decode the XDR result and extract the created contract ID.
func (d *ContractDeployer) extractContractIDFromResult(resultXDR string) (string, error) {
	_ = resultXDR
	return "", nil
}

// UploadWasmOp represents a Soroban upload WASM host function operation.
type UploadWasmOp struct {
	Wasm string `json:"wasm"`
}

// CreateContractOp represents a Soroban create contract host function operation.
type CreateContractOp struct {
	WasmHash string               `json:"wasm_hash"`
	InitArgs []stellar.SorobanArg `json:"init_args"`
	Salt     string               `json:"salt"`
}
