package token

import (
	"context"
	"fmt"
	"strconv"

	"github.com/stellar/go/keypair"

	"github.com/moistello/backend/internal/domain/wallet"
	"github.com/moistello/backend/pkg/crypto"
	"github.com/moistello/backend/pkg/stellar"
	"github.com/moistello/backend/pkg/stellar/soroban"
)

type Service interface {
	GetBalance(ctx context.Context, address string) (uint64, error)
	Stake(ctx context.Context, userID string, amount uint64) (string, error)
	Unstake(ctx context.Context, userID string, amount uint64) (string, error)
	GetStakedAmount(ctx context.Context, address string) (uint64, error)
}

type Config struct {
	GovernanceTokenContractID string
	SorobanRPCURL             string
	NetworkPassphrase         string
	HorizonURL                string
	EncryptionKey             string
}

type service struct {
	walletRepo    wallet.Repository
	cfg           Config
	sorobanClient *soroban.Client
	encryptionKey []byte
}

func NewService(walletRepo wallet.Repository, cfg Config) (Service, error) {
	if cfg.GovernanceTokenContractID == "" {
		return nil, fmt.Errorf("governance token contract ID is required")
	}

	var encKey []byte
	if cfg.EncryptionKey != "" {
		var err error
		encKey, err = crypto.ParseEncryptionKey(cfg.EncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("parsing encryption key: %w", err)
		}
	}

	return &service{
		walletRepo:    walletRepo,
		cfg:           cfg,
		sorobanClient: soroban.NewClient(cfg.SorobanRPCURL),
		encryptionKey: encKey,
	}, nil
}

// readContract simulates a read-only contract call and returns the raw return
// value. Simulation requires no funding or signature, so a throwaway signer
// is used and the sequence falls back to 0 when the source account does not
// exist on chain.
func (s *service) readContract(ctx context.Context, method string, args []stellar.SorobanArg) (any, error) {
	kp, err := keypair.Random()
	if err != nil {
		return nil, fmt.Errorf("generating temporary keypair: %w", err)
	}
	accountMgr := stellar.NewAccountManager(
		stellar.NewClient(s.cfg.HorizonURL, s.cfg.SorobanRPCURL, s.cfg.NetworkPassphrase),
		kp.Address(),
	)

	builder := stellar.NewTransactionBuilder(kp.Address())
	builder.AddSorobanInvoke(s.cfg.GovernanceTokenContractID, method, args)

	seq, err := accountMgr.NextSequence(ctx)
	if err != nil {
		// Unfunded or unreachable source account: 0 is valid for simulation.
		seq = 0
	}
	tx := builder.Build(seq)

	simResult, err := s.sorobanClient.SimulateTransaction(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("simulating %s query: %w", method, err)
	}
	if simResult.Error != nil {
		return nil, fmt.Errorf("contract error: %s", *simResult.Error)
	}
	if simResult.ReturnValue == nil {
		return nil, fmt.Errorf("contract %s returned no value", method)
	}
	return simResult.ReturnValue, nil
}

// GetBalance calls the governance token contract's balance function
func (s *service) GetBalance(ctx context.Context, address string) (uint64, error) {
	value, err := s.readContract(ctx, "balance", []stellar.SorobanArg{{Type: "address", Value: address}})
	if err != nil {
		return 0, err
	}
	return scvalToUint64(value)
}

// GetStakedAmount calls the governance token contract's get_staked_amount function
func (s *service) GetStakedAmount(ctx context.Context, address string) (uint64, error) {
	value, err := s.readContract(ctx, "get_staked_amount", []stellar.SorobanArg{{Type: "address", Value: address}})
	if err != nil {
		return 0, err
	}
	return scvalToUint64(value)
}

// Stake calls the governance token contract's stake function
func (s *service) Stake(ctx context.Context, userID string, amount uint64) (string, error) {
	signer, err := s.userSigner(ctx, userID)
	if err != nil {
		return "", err
	}
	invoker := s.invoker(signer)

	txHash, err := invoker.InvokeFunction(ctx, "stake", amount)
	if err != nil {
		return txHash, fmt.Errorf("executing stake: %w", err)
	}
	return txHash, nil
}

// Unstake calls the governance token contract's unstake function
func (s *service) Unstake(ctx context.Context, userID string, amount uint64) (string, error) {
	signer, err := s.userSigner(ctx, userID)
	if err != nil {
		return "", err
	}
	invoker := s.invoker(signer)

	txHash, err := invoker.InvokeFunction(ctx, "unstake", amount)
	if err != nil {
		return txHash, fmt.Errorf("executing unstake: %w", err)
	}
	return txHash, nil
}

// userSigner resolves the user's wallet, decrypts its secret key, and builds
// the signing keypair for on-chain writes.
func (s *service) userSigner(ctx context.Context, userID string) (*stellar.Signer, error) {
	wallets, err := s.walletRepo.FindByUserID(ctx, userID)
	if err != nil || len(wallets) == 0 {
		return nil, fmt.Errorf("user wallet not found")
	}
	secretKey, err := wallets[0].DecryptSecret(s.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decrypting wallet secret: %w", err)
	}
	signer, err := stellar.NewSigner(secretKey)
	if err != nil {
		return nil, fmt.Errorf("creating signer: %w", err)
	}
	return signer, nil
}

func (s *service) invoker(signer *stellar.Signer) *soroban.ContractInvoker {
	accountMgr := stellar.NewAccountManager(
		stellar.NewClient(s.cfg.HorizonURL, s.cfg.SorobanRPCURL, s.cfg.NetworkPassphrase),
		signer.Address(),
	)
	return soroban.NewContractInvoker(s.sorobanClient, signer, accountMgr, s.cfg.GovernanceTokenContractID)
}

// scvalToUint64 extracts an unsigned integer from a soroban-rpc SCVal JSON
// shape: {"u64":"123"}, {"u32":"123"}, {"u128":{"hi":"0","lo":"123"}}, or the
// signed equivalents (rejecting negatives).
func scvalToUint64(v any) (uint64, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("unexpected return value shape: %T", v)
	}
	for _, key := range []string{"u64", "i64", "u32", "i32"} {
		if raw, ok := obj[key]; ok {
			n, err := strconv.ParseUint(fmt.Sprint(raw), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parsing %s return value %q: %w", key, raw, err)
			}
			return n, nil
		}
	}
	for _, key := range []string{"u128", "i128"} {
		if raw, ok := obj[key]; ok {
			parts, ok := raw.(map[string]any)
			if !ok {
				return 0, fmt.Errorf("unexpected %s return value shape: %T", key, raw)
			}
			hi, err := strconv.ParseUint(fmt.Sprint(parts["hi"]), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parsing %s high bits: %w", key, err)
			}
			lo, err := strconv.ParseUint(fmt.Sprint(parts["lo"]), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parsing %s low bits: %w", key, err)
			}
			if hi > 0 {
				return 0, fmt.Errorf("%s return value exceeds uint64", key)
			}
			return lo, nil
		}
	}
	return 0, fmt.Errorf("no numeric field in return value: %v", v)
}
