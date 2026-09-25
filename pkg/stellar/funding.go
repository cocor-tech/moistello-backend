package stellar

import (
	"context"
	"fmt"
	"time"

	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/keypair"
	"github.com/stellar/go/txnbuild"
)

const (
	// fundRetryAttempts is how many times funding a new wallet account is
	// retried before giving up (transient Horizon failures are common on
	// testnet).
	fundRetryAttempts = 3
	// fundRetryBaseDelay grows linearly across retry attempts.
	fundRetryBaseDelay = 500 * time.Millisecond
)

// BuildFundAccountTx builds and signs a create-account transaction that funds
// destination with startingBalance XLM, using master as the source account.
// sourceSeq must be master's current sequence number as reported by Horizon.
func BuildFundAccountTx(master *keypair.Full, sourceSeq int64, destination, startingBalance, networkPassphrase string) (*txnbuild.Transaction, error) {
	if _, err := keypair.ParseAddress(destination); err != nil {
		return nil, fmt.Errorf("invalid destination address: %w", err)
	}

	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: master.Address(), Sequence: sourceSeq},
		IncrementSequenceNum: true,
		Operations: []txnbuild.Operation{
			&txnbuild.CreateAccount{
				Destination: destination,
				Amount:      startingBalance,
			},
		},
		BaseFee:       txnbuild.MinBaseFee,
		Preconditions: txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
	})
	if err != nil {
		return nil, fmt.Errorf("building fund tx: %w", err)
	}

	tx, err = tx.Sign(networkPassphrase, master)
	if err != nil {
		return nil, fmt.Errorf("signing fund tx: %w", err)
	}
	return tx, nil
}

// FundAccount funds destination with startingBalance XLM from master. It loads
// the master account's sequence from Horizon and submits the create-account
// transaction.
func FundAccount(horizon *horizonclient.Client, master *keypair.Full, destination, startingBalance, networkPassphrase string) error {
	account, err := horizon.AccountDetail(horizonclient.AccountRequest{AccountID: master.Address()})
	if err != nil {
		return fmt.Errorf("loading master account: %w", err)
	}

	tx, err := BuildFundAccountTx(master, account.Sequence, destination, startingBalance, networkPassphrase)
	if err != nil {
		return err
	}

	txe, err := tx.Base64()
	if err != nil {
		return fmt.Errorf("encoding fund tx: %w", err)
	}
	if _, err := horizon.SubmitTransactionXDR(txe); err != nil {
		return fmt.Errorf("submitting fund tx: %w", err)
	}
	return nil
}

// FundAccountWithRetry best-effort funds destination from master, retrying
// transient failures with linear backoff until ctx expires or the attempts run
// out. The wallet service uses this to fund newly created accounts without
// blocking wallet creation (failures are logged and swallowed by the caller).
func FundAccountWithRetry(ctx context.Context, horizon *horizonclient.Client, master *keypair.Full, destination, startingBalance, networkPassphrase string) error {
	var lastErr error
	for attempt := 0; attempt < fundRetryAttempts; attempt++ {
		if err := FundAccount(horizon, master, destination, startingBalance, networkPassphrase); err != nil {
			lastErr = err

			timer := time.NewTimer(fundRetryBaseDelay * time.Duration(attempt+1))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("funding account after %d retries: %w", fundRetryAttempts, lastErr)
}

// BuildChangeTrustTx builds and signs a change-trust transaction that opens a
// credit-asset trustline (e.g. USDC) on the account identified by kp.
// sourceSeq must be the account's current sequence number as reported by
// Horizon.
func BuildChangeTrustTx(kp *keypair.Full, sourceSeq int64, assetCode, assetIssuer, networkPassphrase string) (*txnbuild.Transaction, error) {
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: kp.Address(), Sequence: sourceSeq},
		IncrementSequenceNum: true,
		Operations: []txnbuild.Operation{
			&txnbuild.ChangeTrust{
				Line: txnbuild.ChangeTrustAssetWrapper{
					Asset: txnbuild.CreditAsset{Code: assetCode, Issuer: assetIssuer},
				},
			},
		},
		BaseFee:       txnbuild.MinBaseFee,
		Preconditions: txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
	})
	if err != nil {
		return nil, fmt.Errorf("building trustline tx: %w", err)
	}

	tx, err = tx.Sign(networkPassphrase, kp)
	if err != nil {
		return nil, fmt.Errorf("signing trustline tx: %w", err)
	}
	return tx, nil
}

// SetTrustline opens a credit-asset trustline (e.g. USDC issued by
// assetIssuer) on the account identified by kp. It loads the account sequence
// from Horizon and submits the change-trust transaction.
func SetTrustline(horizon *horizonclient.Client, kp *keypair.Full, assetCode, assetIssuer, networkPassphrase string) error {
	account, err := horizon.AccountDetail(horizonclient.AccountRequest{AccountID: kp.Address()})
	if err != nil {
		return fmt.Errorf("loading account for trustline: %w", err)
	}

	tx, err := BuildChangeTrustTx(kp, account.Sequence, assetCode, assetIssuer, networkPassphrase)
	if err != nil {
		return err
	}

	txe, err := tx.Base64()
	if err != nil {
		return fmt.Errorf("encoding trustline tx: %w", err)
	}
	if _, err := horizon.SubmitTransactionXDR(txe); err != nil {
		return fmt.Errorf("submitting trustline tx: %w", err)
	}
	return nil
}
