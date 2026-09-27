package main

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var reorgCmd = &cobra.Command{
	Use:   "reorg [ledger]",
	Short: "Rewind the indexer cursor to a specific ledger for reorg recovery",
	Long: `Reset the indexer cursor to the specified ledger sequence, allowing the
indexer to re-process events from that point forward. Use this after a
Stellar network reorganization to re-index from the correct fork.

This command:
1. Sets the cursor to the target ledger
2. Deletes contract_events recorded after that ledger
3. Logs the operation for audit trail

Example:
  moistello-indexer reorg 12345678`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		targetLedger, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid ledger number: %w", err)
		}

		db, err := openDB()
		if err != nil {
			return err
		}
		defer db.Close()

		ctx := context.Background()

		// Get current cursor for audit
		var currentLedger int64
		err = db.QueryRowContext(ctx, "SELECT last_ledger FROM indexer_cursor WHERE chain = 'stellar'").Scan(&currentLedger)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("reading current cursor: %w", err)
		}

		log.Info().
			Int64("from_ledger", currentLedger).
			Int64("to_ledger", targetLedger).
			Msg("rewinding indexer cursor for reorg")

		// Delete events after target ledger
		result, err := db.ExecContext(ctx,
			"DELETE FROM contract_events WHERE ledger > $1", targetLedger)
		if err != nil {
			return fmt.Errorf("deleting events after ledger %d: %w", targetLedger, err)
		}
		if deleted, _ := result.RowsAffected(); deleted > 0 {
			log.Info().Int64("deleted_events", deleted).Msg("removed events after target ledger")
		}

		// Update cursor
		_, err = db.ExecContext(ctx,
			"UPDATE indexer_cursor SET last_ledger = $1, last_processed_at = NOW() WHERE chain = 'stellar'",
			targetLedger)
		if err != nil {
			return fmt.Errorf("updating cursor: %w", err)
		}

		log.Info().Int64("new_ledger", targetLedger).Msg("indexer cursor rewound successfully")
		fmt.Printf("Indexer cursor rewound to ledger %d\n", targetLedger)
		return nil
	},
}
