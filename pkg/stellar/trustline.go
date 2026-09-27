package stellar

import (
	"context"
	"fmt"
	"strings"

	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/keypair"
	"github.com/pkg/logger"
)

// TrustlineError represents a trustline-related error during payout operations.
type TrustlineError struct {
	AssetCode  string
	AssetIssuer string
	Address    string
	Message    string
}

func (e *TrustlineError) Error() string {
	return fmt.Sprintf("trustline error for %s on %s: %s", e.AssetCode, e.Address, e.Message)
}

// IsTrustlineMissingError checks if a Horizon error indicates a missing trustline.
func IsTrustlineMissingError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	// Horizon returns these errors when a trustline is missing:
	// - "op_no_trust" — no trustline for the asset
	// - "op_underfunded" — insufficient balance (often after trustline issue)
	// - "transaction failed" with trustline-related result codes
	return strings.Contains(errMsg, "op_no_trust") ||
		strings.Contains(errMsg, "no_trust") ||
		strings.Contains(errMsg, "trustline") ||
		strings.Contains(errMsg, "trust line")
}

// EnsureTrustline checks if the account has a trustline for the given asset
// and creates one if missing. Returns nil if the trustline already exists.
func EnsureTrustline(
	ctx context.Context,
	horizon *horizonclient.Client,
	kp *keypair.Full,
	assetCode, assetIssuer, networkPassphrase string,
) error {
	log := logger.Ctx(ctx)

	account, err := horizon.AccountDetail(horizonclient.AccountRequest{AccountID: kp.Address()})
	if err != nil {
		return fmt.Errorf("loading account for trustline check: %w", err)
	}

	// Check if trustline already exists
	for _, balance := range account.Balances {
		if balance.Asset.Code == assetCode && balance.Asset.Issuer == assetIssuer {
			log.Debug().
				Str("asset", assetCode).
				Str("address", kp.Address()).
				Msg("trustline already exists")
			return nil
		}
	}

	// Create trustline
	log.Info().
		Str("asset", assetCode).
		Str("issuer", assetIssuer).
		Str("address", kp.Address()).
		Msg("creating missing trustline")

	return SetTrustline(horizon, kp, assetCode, assetIssuer, networkPassphrase)
}

// HandleTrustlineError handles trustline-related errors during payout sweep.
// Returns nil if the error is not trustline-related, or wraps it with context.
func HandleTrustlineError(err error, assetCode, address string) error {
	if err == nil {
		return nil
	}
	if !IsTrustlineMissingError(err) {
		return err
	}
	return &TrustlineError{
		AssetCode: assetCode,
		Address:   address,
		Message:   fmt.Sprintf("trustline missing or invalid: %v", err),
	}
}
