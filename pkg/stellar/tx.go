package stellar

import (
	"fmt"
	"time"

	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/keypair"
	"github.com/stellar/go/txnbuild"
)

// SignXDR signs an existing XDR transaction envelope with the given keypair
// and returns the base64-encoded signed envelope.
func SignXDR(xdr string, networkPassphrase string, kp *keypair.Full) (string, error) {
	genericTx, err := txnbuild.TransactionFromXDR(xdr)
	if err != nil {
		return "", fmt.Errorf("parsing transaction XDR: %w", err)
	}

	tx, ok := genericTx.Transaction()
	if !ok {
		return "", fmt.Errorf("unsupported transaction type (expected a regular Transaction, not FeeBump)")
	}

	tx, err = tx.Sign(networkPassphrase, kp)
	if err != nil {
		return "", fmt.Errorf("signing transaction: %w", err)
	}

	signedXDR, err := tx.Base64()
	if err != nil {
		return "", fmt.Errorf("encoding signed XDR: %w", err)
	}

	return signedXDR, nil
}

// PaymentParams holds the parameters for building a Stellar payment transaction.
type PaymentParams struct {
	Destination       string
	AssetCode         string // "XLM" for native asset, otherwise the asset code (e.g. "USDC")
	AssetIssuer       string // issuer for non-native assets
	Amount            float64
	Memo              string
	NetworkPassphrase string
}

// BuildPaymentTx builds, signs, and submits a Stellar payment transaction.
// It loads the source account from Horizon, constructs the payment operation,
// signs it, and submits it. Returns the transaction hash on success.
func BuildPaymentTx(
	horizon *horizonclient.Client,
	kp *keypair.Full,
	params PaymentParams,
) (string, error) {
	account, err := horizon.AccountDetail(horizonclient.AccountRequest{AccountID: kp.Address()})
	if err != nil {
		return "", fmt.Errorf("loading account: %w", err)
	}

	var op txnbuild.Operation
	if params.AssetCode == "XLM" || params.AssetCode == "" {
		op = &txnbuild.Payment{
			Destination: params.Destination,
			Amount:      fmt.Sprintf("%.7f", params.Amount),
			Asset:       txnbuild.NativeAsset{},
		}
	} else {
		op = &txnbuild.Payment{
			Destination: params.Destination,
			Amount:      fmt.Sprintf("%.7f", params.Amount),
			Asset:       txnbuild.CreditAsset{Code: params.AssetCode, Issuer: params.AssetIssuer},
		}
	}

	txParams := txnbuild.TransactionParams{
		SourceAccount:        &account,
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{op},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimebounds(0, time.Now().Unix()+600)},
	}
	if params.Memo != "" {
		txParams.Memo = txnbuild.MemoText(params.Memo)
	}

	tx, err := txnbuild.NewTransaction(txParams)
	if err != nil {
		return "", fmt.Errorf("building tx: %w", err)
	}

	tx, err = tx.Sign(params.NetworkPassphrase, kp)
	if err != nil {
		return "", fmt.Errorf("signing tx: %w", err)
	}

	txe, err := tx.Base64()
	if err != nil {
		return "", fmt.Errorf("encoding tx: %w", err)
	}

	resp, err := horizon.SubmitTransactionXDR(txe)
	if err != nil {
		return "", fmt.Errorf("submitting tx: %w", err)
	}

	return resp.Hash, nil
}
