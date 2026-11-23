package soroban_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/pkg/stellar/soroban"
	"github.com/moistello/backend/pkg/stellar/stellartest"
)

func TestPauseController(t *testing.T) {
	t.Run("Pause success", func(t *testing.T) {
		f := newInvokerFixture(t)
		queueSuccessTx(f.net)

		ctrl := soroban.NewPauseController(f.invoker)
		hash, err := ctrl.Pause(context.Background())
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("Pause error wraps error", func(t *testing.T) {
		f := newInvokerFixture(t)
		f.net.QueueRPC("simulateTransaction", stellartest.RPCResponse{HTTPStatus: 500, RawBody: "down"})

		ctrl := soroban.NewPauseController(f.invoker)
		_, err := ctrl.Pause(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pausing contract:")
	})

	t.Run("Unpause success", func(t *testing.T) {
		f := newInvokerFixture(t)
		queueSuccessTx(f.net)

		ctrl := soroban.NewPauseController(f.invoker)
		hash, err := ctrl.Unpause(context.Background())
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("Unpause error wraps error", func(t *testing.T) {
		f := newInvokerFixture(t)
		f.net.QueueRPC("simulateTransaction", stellartest.RPCResponse{HTTPStatus: 500, RawBody: "down"})

		ctrl := soroban.NewPauseController(f.invoker)
		_, err := ctrl.Unpause(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unpausing contract:")
	})

	t.Run("IsPaused returns false on normal call success", func(t *testing.T) {
		f := newInvokerFixture(t)
		queueSuccessTx(f.net)

		ctrl := soroban.NewPauseController(f.invoker)
		paused, err := ctrl.IsPaused(context.Background())
		require.NoError(t, err)
		assert.False(t, paused)
	})

	t.Run("IsPaused returns true when ContractPaused error code detected", func(t *testing.T) {
		f := newInvokerFixture(t)
		f.net.SetSimulateResult(map[string]any{
			"error":  "HostError: ContractPaused",
			"events": []string{},
			"cost":   map[string]any{},
		})

		ctrl := soroban.NewPauseController(f.invoker)
		paused, err := ctrl.IsPaused(context.Background())
		require.NoError(t, err)
		assert.True(t, paused)
	})

	t.Run("IsPaused returns error on unexpected contract failure", func(t *testing.T) {
		f := newInvokerFixture(t)
		f.net.SetSimulateResult(map[string]any{
			"error":  "HostError: UnknownFailure",
			"events": []string{},
			"cost":   map[string]any{},
		})

		ctrl := soroban.NewPauseController(f.invoker)
		paused, err := ctrl.IsPaused(context.Background())
		require.Error(t, err)
		assert.False(t, paused)
		assert.Contains(t, err.Error(), "checking pause status:")
	})
}
