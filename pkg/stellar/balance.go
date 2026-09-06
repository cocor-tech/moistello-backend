package stellar

import (
	"fmt"

	"github.com/stellar/go/clients/horizonclient"
)

// AccountBalance holds parsed XLM and USDC balances for a Stellar account.
type AccountBalance struct {
	XLM  string
	USDC string
}

// GetBalance fetches the XLM and USDC balance for a given Stellar public key.
// usdcIssuer is the Stellar address of the USDC asset issuer to match against.
func GetBalance(horizon *horizonclient.Client, publicKey, usdcIssuer string) (*AccountBalance, error) {
	account, err := horizon.AccountDetail(horizonclient.AccountRequest{AccountID: publicKey})
	if err != nil {
		return nil, fmt.Errorf("fetching account from horizon: %w", err)
	}

	bal := &AccountBalance{XLM: "0.0000", USDC: "0.0000"}
	for _, b := range account.Balances {
		if b.Asset.Type == "native" {
			bal.XLM = b.Balance
		} else if b.Asset.Code == "USDC" && b.Asset.Issuer == usdcIssuer {
			bal.USDC = b.Balance
		}
	}
	return bal, nil
}
