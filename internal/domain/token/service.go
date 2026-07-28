package token

import (
	"context"
	"fmt"

	"github.com/stellar/go/keypair"
	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/pkg/stellar"
	"github.com/moistello/backend/pkg/stellar/soroban"
)

type Service interface {
	GetBalance(ctx context.Context, address string) (uint64, error)
	Stake(ctx context.Context, userID string, passkeySeed []byte, amount uint64) (string, error)
	Unstake(ctx context.Context, userID string, passkeySeed []byte, amount uint64) (string, error)
	GetStakedAmount(ctx context.Context, address string) (uint64, error)
}

type Config struct {
	GovernanceTokenContractID string
	SorobanRPCURL             string
	NetworkPassphrase         string
	HorizonURL                string
}

type service struct {
	walletRepo   wallet.Repository
	cfg          Config
	sorobanClient *soroban.Client
}

func NewService(walletRepo wallet.Repository, cfg Config) (Service, error) {
	if cfg.GovernanceTokenContractID == "" {
		return nil, fmt.Errorf("governance token contract ID is required")
	}

	sorobanClient := soroban.NewClient(cfg.SorobanRPCURL)

	return &service{
		walletRepo:   walletRepo,
		cfg:          cfg,
		sorobanClient: sorobanClient,
	}, nil
}

// GetBalance calls the governance token contract's balance function
func (s *service) GetBalance(ctx context.Context, address string) (uint64, error) {
	// Create contract invoker to read from the contract
	// For read-only calls, we can simulate which is sufficient
	kp, err := keypair.Random()
	if err != nil {
		return 0, fmt.Errorf("generating temporary keypair: %w", err)
	}

	signer := stellar.NewSigner(kp)
	accountMgr := stellar.NewAccountManager(kp.Address(), s.cfg.HorizonURL)
	invoker := soroban.NewContractInvoker(s.sorobanClient, signer, accountMgr, s.cfg.GovernanceTokenContractID)

	// Simulate the balance query - this is a read-only operation
	// Typically, for Soroban contract reads, we use simulate which returns the value without submitting
	builder := stellar.NewTransactionBuilder(kp.Address())
	builder.AddSorobanInvoke(s.cfg.GovernanceTokenContractID, "balance", []stellar.SorobanArg{
		{Type: "address", Value: address},
	})

	// Get sequence number (required even for simulation)
	seq, err := accountMgr.NextSequence(ctx)
	if err != nil {
		return 0, fmt.Errorf("getting sequence: %w", err)
	}
	tx := builder.Build(seq)

	simResult, err := s.sorobanClient.SimulateTransaction(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("simulating balance query: %w", err)
	}
	if simResult.Error != nil {
		return 0, fmt.Errorf("contract error: %s", *simResult.Error)
	}

	// Parse the balance from simulation result
	// In a real implementation, you'd properly parse the return value from simResult
	// This is a placeholder - actual parsing depends on your contract's return format
	balance := uint64(0)
	if val, ok := simResult.ReturnValue.(uint64); ok {
		balance = val
	}

	return balance, nil
}

// GetStakedAmount calls the governance token contract's get_staked_amount function
func (s *service) GetStakedAmount(ctx context.Context, address string) (uint64, error) {
	// Similar to GetBalance, simulate the staked amount query
	kp, err := keypair.Random()
	if err != nil {
		return 0, fmt.Errorf("generating temporary keypair: %w", err)
	}

	signer := stellar.NewSigner(kp)
	accountMgr := stellar.NewAccountManager(kp.Address(), s.cfg.HorizonURL)
	invoker := soroban.NewContractInvoker(s.sorobanClient, signer, accountMgr, s.cfg.GovernanceTokenContractID)

	builder := stellar.NewTransactionBuilder(kp.Address())
	builder.AddSorobanInvoke(s.cfg.GovernanceTokenContractID, "get_staked_amount", []stellar.SorobanArg{
		{Type: "address", Value: address},
	})

	seq, err := accountMgr.NextSequence(ctx)
	if err != nil {
		return 0, fmt.Errorf("getting sequence: %w", err)
	}
	tx := builder.Build(seq)

	simResult, err := s.sorobanClient.SimulateTransaction(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("simulating staked amount query: %w", err)
	}
	if simResult.Error != nil {
		return 0, fmt.Errorf("contract error: %s", *simResult.Error)
	}

	stakedAmount := uint64(0)
	if val, ok := simResult.ReturnValue.(uint64); ok {
		stakedAmount = val
	}

	return stakedAmount, nil
}

// Stake calls the governance token contract's stake function
func (s *service) Stake(ctx context.Context, userID string, passkeySeed []byte, amount uint64) (string, error) {
	// Get the user's wallet
	wallets, err := s.walletRepo.GetWallets(ctx, userID)
	if err != nil || len(wallets) == 0 {
		return "", fmt.Errorf("user wallet not found")
	}
	wallet := wallets[0]

	// Decrypt the wallet's secret key
	secretKey, err := wallet.DecryptSecret(passkeySeed)
	if err != nil {
		return "", fmt.Errorf("decrypting wallet secret: %w", err)
	}

	kp, err := keypair.ParseFull(secretKey)
	if err != nil {
		return "", fmt.Errorf("parsing keypair: %w", err)
	}

	// Create contract invoker with the user's wallet as signer
	signer := stellar.NewSigner(kp)
	accountMgr := stellar.NewAccountManager(kp.Address(), s.cfg.HorizonURL)
	invoker := soroban.NewContractInvoker(s.sorobanClient, signer, accountMgr, s.cfg.GovernanceTokenContractID)

	// Execute the stake call
	txHash, err := invoker.InvokeFunction(ctx, "stake", amount)
	if err != nil {
		return txHash, fmt.Errorf("executing stake: %w", err)
	}

	return txHash, nil
}

// Unstake calls the governance token contract's unstake function
func (s *service) Unstake(ctx context.Context, userID string, passkeySeed []byte, amount uint64) (string, error) {
	// Get the user's wallet
	wallets, err := s.walletRepo.GetWallets(ctx, userID)
	if err != nil || len(wallets) == 0 {
		return "", fmt.Errorf("user wallet not found")
	}
	wallet := wallets[0]

	// Decrypt the wallet's secret key
	secretKey, err := wallet.DecryptSecret(passkeySeed)
	if err != nil {
		return "", fmt.Errorf("decrypting wallet secret: %w", err)
	}

	kp, err := keypair.ParseFull(secretKey)
	if err != nil {
		return "", fmt.Errorf("parsing keypair: %w", err)
	}

	// Create contract invoker with the user's wallet as signer
	signer := stellar.NewSigner(kp)
	accountMgr := stellar.NewAccountManager(kp.Address(), s.cfg.HorizonURL)
	invoker := soroban.NewContractInvoker(s.sorobanClient, signer, accountMgr, s.cfg.GovernanceTokenContractID)

	// Execute the unstake call
	txHash, err := invoker.InvokeFunction(ctx, "unstake", amount)
	if err != nil {
		return txHash, fmt.Errorf("executing unstake: %w", err)
	}

	return txHash, nil
}