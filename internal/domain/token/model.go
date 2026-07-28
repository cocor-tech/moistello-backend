package token

// Token represents the governance token
type Token struct {
	// ContractID is the Soroban contract ID of the governance token
	ContractID string `json:"contract_id"`
	// Name of the token
	Name string `json:"name"`
	// Symbol of the token
	Symbol string `json:"symbol"`
	// Decimals places for the token
	Decimals uint8 `json:"decimals"`
	// Total supply of the token
	TotalSupply uint64 `json:"total_supply"`
}

// Stake represents a user's stake
type Stake struct {
	// Address of the staker
	Address string `json:"address"`
	// Amount staked
	Amount uint64 `json:"amount"`
	// Timestamp when the stake was created
	CreatedAt int64 `json:"created_at"`
	// Timestamp when the stake can be unlocked (if applicable)
	UnlockAt int64 `json:"unlock_at,omitempty"`
}

// BalanceResponse represents the balance response
type BalanceResponse struct {
	Address string `json:"address"`
	Balance uint64 `json:"balance"`
}

// StakeRequest represents a stake/unstake request
type StakeRequest struct {
	Amount      uint64 `json:"amount" binding:"required,gt=0"`
	PasskeySeed string `json:"passkeySeed" binding:"required"`
}

// StakeResponse represents the response after staking/unstaking
type StakeResponse struct {
	TxHash string `json:"tx_hash"`
	Amount uint64 `json:"amount"`
	Success bool `json:"success"`
}