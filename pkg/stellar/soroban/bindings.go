package soroban

import (
	"context"
	"fmt"

	"github.com/moistello/backend/pkg/stellar"
)

// ── Circle Factory Bindings ──
// Manages the lifecycle of Moistello Circles.

type CircleFactoryClient struct {
	invoker *ContractInvoker
}

func NewCircleFactoryClient(invoker *ContractInvoker) *CircleFactoryClient {
	return &CircleFactoryClient{invoker: invoker}
}

func (c *CircleFactoryClient) DeployCircle(ctx context.Context, name, description, circleType string, payoutType string, amount int64, currency string, frequency string, maxMembers uint32) (string, error) {
	args := []stellar.SorobanArg{
		{Type: "symbol", Value: name},
		{Type: "symbol", Value: description},
		{Type: "symbol", Value: circleType},
		{Type: "symbol", Value: payoutType},
		{Type: "i128", Value: fmt.Sprintf("%d", amount)},
		{Type: "symbol", Value: currency},
		{Type: "symbol", Value: frequency},
		{Type: "u32", Value: fmt.Sprintf("%d", maxMembers)},
	}
	return c.invoker.ExecuteContractCall(ctx, "deploy_circle", args)
}

func (c *CircleFactoryClient) GetCircles(ctx context.Context, admin string) (string, error) {
	return c.invoker.ExecuteContractCall(ctx, "get_circles", []stellar.SorobanArg{{Type: "address", Value: admin}})
}

func (c *CircleFactoryClient) SetFee(ctx context.Context, feeBips uint32) (string, error) {
	return c.invoker.ExecuteContractCall(ctx, "set_fee", []stellar.SorobanArg{{Type: "u32", Value: fmt.Sprintf("%d", feeBips)}})
}

func (c *CircleFactoryClient) GetAllCircles(ctx context.Context) (string, error) {
	return c.invoker.ExecuteContractCall(ctx, "get_all_circles", nil)
}

func (c *CircleFactoryClient) GetCircleCount(ctx context.Context) (string, error) {
	return c.invoker.ExecuteContractCall(ctx, "get_circle_count", nil)
}

// ── Circle Bindings ──
// Individual Moistello Circle operations.

type CircleClient struct {
	invoker *ContractInvoker
}

func NewCircleClient(invoker *ContractInvoker) *CircleClient {
	return &CircleClient{invoker: invoker}
}

func (c *CircleClient) Join(ctx context.Context, member, inviteCode string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "join", member, inviteCode)
}

func (c *CircleClient) Contribute(ctx context.Context, member string, amount int64, round uint32) (string, error) {
	return c.invoker.InvokeFunction(ctx, "contribute", member, amount, round)
}

func (c *CircleClient) TriggerPayout(ctx context.Context, round uint32) (string, error) {
	return c.invoker.InvokeFunction(ctx, "trigger_payout", round)
}

func (c *CircleClient) AuctionBid(ctx context.Context, bidder string, discountBips uint32) (string, error) {
	return c.invoker.InvokeFunction(ctx, "auction_bid", bidder, discountBips)
}

func (c *CircleClient) VotePayout(ctx context.Context, voter, voteFor string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "vote_payout", voter, voteFor)
}

func (c *CircleClient) ExitCircle(ctx context.Context, member string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "exit_circle", member)
}

func (c *CircleClient) ReportLate(ctx context.Context, member string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "report_late", member)
}

func (c *CircleClient) RaiseDispute(ctx context.Context, member, evidence string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "raise_dispute", member, evidence)
}

func (c *CircleClient) ResolveDispute(ctx context.Context, disputeID string, resolution string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "resolve_dispute", disputeID, resolution)
}

func (c *CircleClient) GetMember(ctx context.Context, member string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "get_member", member)
}

// ── Escrow Swap Bindings ──
// P2P swap escrow contract for circle members.

type EscrowSwapClient struct {
	invoker *ContractInvoker
}

func NewEscrowSwapClient(invoker *ContractInvoker) *EscrowSwapClient {
	return &EscrowSwapClient{invoker: invoker}
}

func (c *EscrowSwapClient) CreateSwap(ctx context.Context, circleID, offeror, offeree string, offerorAsset string, offerorAmount int64, requestedAsset string, requestedAmount int64, expiresAt uint64) (string, error) {
	return c.invoker.InvokeFunction(ctx, "create_swap", circleID, offeror, offeree, offerorAsset, offerorAmount, requestedAsset, requestedAmount, expiresAt)
}

func (c *EscrowSwapClient) AcceptSwap(ctx context.Context, swapID string, acceptor string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "accept_swap", swapID, acceptor)
}

func (c *EscrowSwapClient) CancelSwap(ctx context.Context, swapID string, canceller string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "cancel_swap", swapID, canceller)
}

func (c *EscrowSwapClient) ExecuteSwap(ctx context.Context, swapID string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "execute_swap", swapID)
}

func (c *EscrowSwapClient) GetSwap(ctx context.Context, swapID string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "get_swap", swapID)
}

func (c *EscrowSwapClient) GetCircleSwaps(ctx context.Context, circleID string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "get_circle_swaps", circleID)
}

func (c *EscrowSwapClient) GetUserSwaps(ctx context.Context, userID string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "get_user_swaps", userID)
}

func (c *CircleClient) GetRound(ctx context.Context, round uint32) (string, error) {
	return c.invoker.InvokeFunction(ctx, "get_round", round)
}

func (c *CircleClient) GetAuctionState(ctx context.Context) (string, error) {
	return c.invoker.ExecuteContractCall(ctx, "get_auction_state", nil)
}

// ── Reputation Registry Bindings ──
// Tracks member reputation and activity history.

type ReputationClient struct {
	invoker *ContractInvoker
}

func NewReputationClient(invoker *ContractInvoker) *ReputationClient {
	return &ReputationClient{invoker: invoker}
}

func (c *ReputationClient) RecordActivity(ctx context.Context, member, activityType, circleID string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "record_activity", member, activityType, circleID)
}

func (c *ReputationClient) GetScore(ctx context.Context, member string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "get_score", member)
}

func (c *ReputationClient) GetHistory(ctx context.Context, member string, limit uint32) (string, error) {
	return c.invoker.InvokeFunction(ctx, "get_history", member, limit)
}

func (c *ReputationClient) RecordLatePayment(ctx context.Context, member, circleID string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "record_late_payment", member, circleID)
}

func (c *ReputationClient) RecordDefault(ctx context.Context, member, circleID string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "record_default", member, circleID)
}

func (c *ReputationClient) CalculateTrustScore(ctx context.Context, member string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "calculate_trust_score", member)
}

// ── Governance Token Bindings ──
// ERC-style token for circle governance voting.

type GovernanceTokenClient struct {
	invoker *ContractInvoker
}

func NewGovernanceTokenClient(invoker *ContractInvoker) *GovernanceTokenClient {
	return &GovernanceTokenClient{invoker: invoker}
}

func (c *GovernanceTokenClient) Mint(ctx context.Context, to string, amount int64) (string, error) {
	return c.invoker.InvokeFunction(ctx, "mint", to, amount)
}

func (c *GovernanceTokenClient) Transfer(ctx context.Context, from, to string, amount int64) (string, error) {
	return c.invoker.InvokeFunction(ctx, "transfer", from, to, amount)
}

func (c *GovernanceTokenClient) TransferFrom(ctx context.Context, spender, from, to string, amount int64) (string, error) {
	return c.invoker.InvokeFunction(ctx, "transfer_from", spender, from, to, amount)
}

func (c *GovernanceTokenClient) Balance(ctx context.Context, owner string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "balance", owner)
}

func (c *GovernanceTokenClient) Approve(ctx context.Context, from, spender string, amount int64, expirationLedger uint32) (string, error) {
	return c.invoker.InvokeFunction(ctx, "approve", from, spender, amount, expirationLedger)
}

func (c *GovernanceTokenClient) Allowance(ctx context.Context, from, spender string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "allowance", from, spender)
}

func (c *GovernanceTokenClient) Burn(ctx context.Context, from string, amount int64) (string, error) {
	return c.invoker.InvokeFunction(ctx, "burn", from, amount)
}

// ── Treasury Bindings ──
// Manages platform fees, payouts, and vault logic.

type TreasuryClient struct {
	invoker *ContractInvoker
}

func NewTreasuryClient(invoker *ContractInvoker) *TreasuryClient {
	return &TreasuryClient{invoker: invoker}
}

func (c *TreasuryClient) DepositFee(ctx context.Context, from string, amount int64) (string, error) {
	return c.invoker.InvokeFunction(ctx, "deposit_fee", from, amount)
}

func (c *TreasuryClient) Withdraw(ctx context.Context, to string, amount int64) (string, error) {
	return c.invoker.InvokeFunction(ctx, "withdraw", to, amount)
}

func (c *TreasuryClient) GetBalance(ctx context.Context) (string, error) {
	return c.invoker.ExecuteContractCall(ctx, "get_balance", nil)
}

func (c *TreasuryClient) DisbursePayout(ctx context.Context, recipient string, amount int64, circleID string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "disburse_payout", recipient, amount, circleID)
}

func (c *TreasuryClient) CollectCircleFees(ctx context.Context, circleID string, amounts []int64, members []string) (string, error) {
	args := make([]interface{}, 0, 2+len(amounts)+len(members))
	args = append(args, circleID)
	args = append(args, len(amounts))
	for _, a := range amounts {
		args = append(args, a)
	}
	for _, m := range members {
		args = append(args, m)
	}
	return c.invoker.InvokeFunction(ctx, "collect_circle_fees", args...)
}

func (c *TreasuryClient) GetFeeBalance(ctx context.Context, circleID string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "get_fee_balance", circleID)
}

func (c *TreasuryClient) SetFeeRecipient(ctx context.Context, recipient string) (string, error) {
	return c.invoker.InvokeFunction(ctx, "set_fee_recipient", recipient)
}