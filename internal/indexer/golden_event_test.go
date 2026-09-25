package indexer_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/moistello/backend/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var updateGoldens = flag.Bool("update", false, "update golden snapshot files")

func TestGolden_EventDecoding(t *testing.T) {
	testCases := []struct {
		name       string
		event      indexer.ContractEvent
		goldenFile string
	}{
		{
			name: "CircleCreated",
			event: indexer.ContractEvent{
				ContractID: "CCIRCLE1234567890",
				EventType:  indexer.EventCircleCreated,
				Ledger:     123456,
				TxHash:     "tx-circle-created-123",
				Payload: map[string]any{
					"circle_id":   "550e8400-e29b-41d4-a716-446655440000",
					"creator":     "GCREATOR123",
					"config_hash": "a1b2c3d4e5f6",
				},
			},
			goldenFile: "circle_created.golden.json",
		},
		{
			name: "ContributionReceived",
			event: indexer.ContractEvent{
				ContractID: "CCIRCLE1234567890",
				EventType:  indexer.EventContributionReceived,
				Ledger:     123457,
				TxHash:     "tx-contrib-123",
				Payload: map[string]any{
					"circle_id": "550e8400-e29b-41d4-a716-446655440000",
					"member":    "GMEMBER123",
					"amount":    100.0,
					"round":     float64(1),
				},
			},
			goldenFile: "contribution_received.golden.json",
		},
		{
			name: "PayoutExecuted",
			event: indexer.ContractEvent{
				ContractID: "CCIRCLE1234567890",
				EventType:  indexer.EventPayoutExecuted,
				Ledger:     123458,
				TxHash:     "tx-payout-123",
				Payload: map[string]any{
					"circle_id":  "550e8400-e29b-41d4-a716-446655440000",
					"recipient":  "GRECIPIENT123",
					"amount":     500.0,
					"fee_amount": 5.0,
					"round":      float64(1),
				},
			},
			goldenFile: "payout_executed.golden.json",
		},
	}

	testDataDir := filepath.Join("testdata")
	_ = os.MkdirAll(testDataDir, 0755)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			filePath := filepath.Join(testDataDir, tc.goldenFile)
			actualBytes, err := json.MarshalIndent(tc.event, "", "  ")
			require.NoError(t, err)

			if *updateGoldens {
				err := os.WriteFile(filePath, actualBytes, 0644)
				require.NoError(t, err)
			}

			expectedBytes, err := os.ReadFile(filePath)
			require.NoError(t, err, "golden file must exist; run with -update to generate")

			var expected, actual indexer.ContractEvent
			require.NoError(t, json.Unmarshal(expectedBytes, &expected))
			require.NoError(t, json.Unmarshal(actualBytes, &actual))

			assert.Equal(t, expected, actual)
		})
	}
}
