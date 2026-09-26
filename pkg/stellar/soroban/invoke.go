package soroban

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/moistello/backend/pkg/money"
	"github.com/moistello/backend/pkg/stellar"
)

// ContractInvoker builds and executes Soroban contract calls.
type ContractInvoker struct {
	client     *Client
	signer     *stellar.Signer
	accountMgr *stellar.AccountManager
	contractID string
}

func NewContractInvoker(client *Client, signer *stellar.Signer, accountMgr *stellar.AccountManager, contractID string) *ContractInvoker {
	return &ContractInvoker{
		client:     client,
		signer:     signer,
		accountMgr: accountMgr,
		contractID: contractID,
	}
}

// ExecuteContractCall performs the full lifecycle:
// Build → Simulate → Sign → Submit → Poll
func (c *ContractInvoker) ExecuteContractCall(ctx context.Context, method string, args []stellar.SorobanArg) (string, error) {
	// 1. BUILD
	builder := stellar.NewTransactionBuilder(c.accountMgr.PublicKey())
	builder.AddSorobanInvoke(c.contractID, method, args)

	seq, err := c.accountMgr.NextSequence(ctx)
	if err != nil {
		return "", fmt.Errorf("getting sequence: %w", err)
	}
	tx := builder.Build(seq)

	// 2. SIMULATE
	simResult, err := c.client.SimulateTransaction(ctx, tx)
	if err != nil {
		return "", fmt.Errorf("simulation failed: %w", err)
	}
	if simResult.Error != nil {
		return "", fmt.Errorf("contract error: %s", *simResult.Error)
	}

	// 3. APPLY SIMULATION (resource estimates → fee)
	tx = c.applyResources(tx, simResult)

	// 4. SIGN
	signedEnvelope, err := c.signTransaction(tx, "Test SDF Network ; September 2015")
	if err != nil {
		return "", fmt.Errorf("signing transaction: %w", err)
	}

	// 5. SUBMIT + POLL
	result, err := c.client.SendTransaction(ctx, signedEnvelope)
	if err != nil {
		if result != nil {
			return result.Hash, err
		}
		return "", err
	}
	if !result.Successful {
		return result.Hash, fmt.Errorf("transaction failed: %s", result.ResultXDR)
	}

	return result.Hash, nil
}

// InvokeFunction calls a contract function with typed arguments.
func (c *ContractInvoker) InvokeFunction(ctx context.Context, method string, args ...interface{}) (string, error) {
	sorobanArgs := make([]stellar.SorobanArg, len(args))
	for i, arg := range args {
		sorobanArgs[i] = toSorobanArg(arg)
	}
	return c.ExecuteContractCall(ctx, method, sorobanArgs)
}

// applyResources applies simulation resource estimates to the transaction fee.
func (c *ContractInvoker) applyResources(tx *stellar.Transaction, result *stellar.SimulationResult) *stellar.Transaction {
	costFactor := int64(result.Cost.CPUInstructions/10000 + result.Cost.MemoryBytes/100)
	if costFactor == 0 {
		costFactor = 1
	}
	if costFactor > 1000 {
		costFactor = 1000
	}
	tx.Fee = tx.Fee * costFactor
	if tx.Fee < 100 {
		tx.Fee = 100
	}
	return tx
}

// signTransaction produces a base64-encoded signed transaction envelope.
// Signs against the network passphrase using Ed25519.
func (c *ContractInvoker) signTransaction(tx *stellar.Transaction, networkPassphrase string) (string, error) {
	txJSON, err := tx.ToJSON()
	if err != nil {
		return "", fmt.Errorf("marshaling tx for signing: %w", err)
	}

	networkID := sha256.Sum256([]byte(networkPassphrase))
	txHash := sha256.Sum256(txJSON)

	payload := make([]byte, len(networkID)+len(txHash))
	copy(payload, networkID[:])
	copy(payload[len(networkID):], txHash[:])

	payloadHash := sha256.Sum256(payload)

	sig, err := c.signer.Sign(payloadHash[:])
	if err != nil {
		return "", fmt.Errorf("ed25519 sign: %w", err)
	}

	envelope := signedEnvelope{
		Tx:         string(txJSON),
		Signatures: []string{base64.StdEncoding.EncodeToString(sig)},
		Network:    networkPassphrase,
	}

	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshaling envelope: %w", err)
	}
	return base64.StdEncoding.EncodeToString(envelopeJSON), nil
}

type signedEnvelope struct {
	Tx         string   `json:"tx"`
	Signatures []string `json:"signatures"`
	Network    string   `json:"network"`
}

func (c *ContractInvoker) ContractID() string { return c.contractID }

// toSorobanArg converts a Go value to a Soroban argument.
func toSorobanArg(v interface{}) stellar.SorobanArg {
	switch val := v.(type) {
	case string:
		if len(val) == 56 && val[0] == 'G' {
			return stellar.SorobanArg{Type: "address", Value: val}
		}
		return stellar.SorobanArg{Type: "symbol", Value: val}
	case int:
		return stellar.SorobanArg{Type: "i128", Value: fmt.Sprintf("%d", val)}
	case int64:
		return stellar.SorobanArg{Type: "i128", Value: fmt.Sprintf("%d", val)}
	case int32:
		return stellar.SorobanArg{Type: "i32", Value: fmt.Sprintf("%d", val)}
	case uint32:
		return stellar.SorobanArg{Type: "u32", Value: fmt.Sprintf("%d", val)}
	case uint64:
		return stellar.SorobanArg{Type: "u64", Value: fmt.Sprintf("%d", val)}
	case money.Money:
		return stellar.SorobanArg{Type: "i128", Value: fmt.Sprintf("%d", val.Stroops())}
	case float64:
		// A float is a whole-token amount; contracts take base units.
		amount, err := money.FromFloat64(val)
		if err != nil {
			return stellar.SorobanArg{Type: "i128", Value: "0"}
		}
		return stellar.SorobanArg{Type: "i128", Value: fmt.Sprintf("%d", amount.Stroops())}
	case bool:
		return stellar.SorobanArg{Type: "bool", Value: fmt.Sprintf("%v", val)}
	case []byte:
		return stellar.SorobanArg{Type: "bytes", Value: base64.StdEncoding.EncodeToString(val)}
	default:
		return stellar.SorobanArg{Type: "symbol", Value: fmt.Sprintf("%v", val)}
	}
}
