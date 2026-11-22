package soroban_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/stellar/soroban"
	"github.com/moistello/backend/pkg/stellar/stellartest"
)

func queueSuccessTx(net *stellartest.Network) {
	net.QueueRPC("getTransaction", stellartest.RPCResponse{
		Result: map[string]any{
			"status":    "SUCCESS",
			"resultXdr": "AAAA",
			"ledger":    100,
		},
	})
}

func TestCircleFactoryClient(t *testing.T) {
	f := newInvokerFixture(t)
	client := soroban.NewCircleFactoryClient(f.invoker)

	t.Run("DeployCircle", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.DeployCircle(context.Background(), "Circle1", "Test Circle", "fixed", "random", 1000000, "USDC", "weekly", 10)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetCircles", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetCircles(context.Background(), f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("SetFee", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.SetFee(context.Background(), 50)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetAllCircles", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetAllCircles(context.Background())
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetCircleCount", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetCircleCount(context.Background())
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})
}

func TestCircleClient(t *testing.T) {
	f := newInvokerFixture(t)
	client := soroban.NewCircleClient(f.invoker)

	t.Run("Join", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.Join(context.Background(), f.address, "INVITE123")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("Contribute", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.Contribute(context.Background(), f.address, 500000, 1)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("TriggerPayout", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.TriggerPayout(context.Background(), 1)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("AuctionBid", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.AuctionBid(context.Background(), f.address, 250)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("VotePayout", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.VotePayout(context.Background(), f.address, f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("ExitCircle", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.ExitCircle(context.Background(), f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("ReportLate", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.ReportLate(context.Background(), f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("RaiseDispute", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.RaiseDispute(context.Background(), f.address, "evidence data")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("ResolveDispute", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.ResolveDispute(context.Background(), "disp-1", "resolved_valid")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetMember", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetMember(context.Background(), f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetRound", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetRound(context.Background(), 2)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetAuctionState", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetAuctionState(context.Background())
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})
}

func TestEscrowSwapClient(t *testing.T) {
	f := newInvokerFixture(t)
	client := soroban.NewEscrowSwapClient(f.invoker)

	t.Run("CreateSwap", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.CreateSwap(context.Background(), "circle-1", f.address, f.address, "USDC", 1000, "XLM", 2000, 1700000000)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("AcceptSwap", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.AcceptSwap(context.Background(), "swap-1", f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("CancelSwap", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.CancelSwap(context.Background(), "swap-1", f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("ExecuteSwap", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.ExecuteSwap(context.Background(), "swap-1")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetSwap", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetSwap(context.Background(), "swap-1")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetCircleSwaps", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetCircleSwaps(context.Background(), "circle-1")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetUserSwaps", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetUserSwaps(context.Background(), f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})
}

func TestReputationClient(t *testing.T) {
	f := newInvokerFixture(t)
	client := soroban.NewReputationClient(f.invoker)

	t.Run("RecordActivity", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.RecordActivity(context.Background(), f.address, "on_time_contribution", "circle-1")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetScore", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetScore(context.Background(), f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetHistory", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetHistory(context.Background(), f.address, 10)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("RecordLatePayment", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.RecordLatePayment(context.Background(), f.address, "circle-1")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("RecordDefault", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.RecordDefault(context.Background(), f.address, "circle-1")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("CalculateTrustScore", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.CalculateTrustScore(context.Background(), f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})
}

func TestGovernanceTokenClient(t *testing.T) {
	f := newInvokerFixture(t)
	client := soroban.NewGovernanceTokenClient(f.invoker)

	t.Run("Mint", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.Mint(context.Background(), f.address, 1000)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("Transfer", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.Transfer(context.Background(), f.address, f.address, 500)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("TransferFrom", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.TransferFrom(context.Background(), f.address, f.address, f.address, 200)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("Balance", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.Balance(context.Background(), f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("Approve", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.Approve(context.Background(), f.address, f.address, 1000, 50000)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("Allowance", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.Allowance(context.Background(), f.address, f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("Burn", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.Burn(context.Background(), f.address, 100)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})
}

func TestTreasuryClient(t *testing.T) {
	f := newInvokerFixture(t)
	client := soroban.NewTreasuryClient(f.invoker)

	t.Run("DepositFee", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.DepositFee(context.Background(), f.address, 1500)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("Withdraw", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.Withdraw(context.Background(), f.address, 500)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetBalance", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetBalance(context.Background())
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("DisbursePayout", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.DisbursePayout(context.Background(), f.address, 10000, "circle-1")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("CollectCircleFees", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.CollectCircleFees(context.Background(), "circle-1", []int64{100, 200}, []string{f.address, f.address})
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("GetFeeBalance", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.GetFeeBalance(context.Background(), "circle-1")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("SetFeeRecipient", func(t *testing.T) {
		queueSuccessTx(f.net)
		hash, err := client.SetFeeRecipient(context.Background(), f.address)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})
}
