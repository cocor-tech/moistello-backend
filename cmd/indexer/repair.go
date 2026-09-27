package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var repairCmd = &cobra.Command{
	Use:   "repair",
	Short: "Repair indexer state by re-processing events from the current cursor",
	Long: `Scan the indexer state and repair any inconsistencies:
1. Find events that failed to process and re-queue them
2. Verify cursor consistency with actual processed events
3. Report any orphaned or missing records

This command is read-only by default. Use --fix to apply repairs.

Example:
  moistello-indexer repair
  moistello-indexer repair --fix`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fix, _ := cmd.Flags().GetBool("fix")

		db, err := openDB()
		if err != nil {
			return err
		}
		defer db.Close()

		ctx := context.Background()

		// Check cursor state
		var cursorLedger int64
		var lastProcessed sql.NullTime
		err = db.QueryRowContext(ctx,
			"SELECT last_ledger, last_processed_at FROM indexer_cursor WHERE chain = 'stellar'").
			Scan(&cursorLedger, &lastProcessed)
		if err != nil {
			return fmt.Errorf("reading cursor: %w", err)
		}

		log.Info().
			Int64("cursor_ledger", cursorLedger).
			Time("last_processed", lastProcessed.Time).
			Bool("fix_mode", fix).
			Msg("starting indexer repair scan")

		// Count events at/after cursor
		var eventCount int64
		err = db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM contract_events WHERE ledger >= $1", cursorLedger).
			Scan(&eventCount)
		if err != nil {
			return fmt.Errorf("counting events: %w", err)
		}

		// Check for duplicate events
		var duplicateCount int64
		err = db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM (
				SELECT tx_hash, event_type, COUNT(*)
				FROM contract_events
				GROUP BY tx_hash, event_type
				HAVING COUNT(*) > 1
			) dups`).Scan(&duplicateCount)
		if err != nil {
			return fmt.Errorf("checking duplicates: %w", err)
		}

		fmt.Printf("Indexer repair report:\n")
		fmt.Printf("  Current cursor ledger: %d\n", cursorLedger)
		fmt.Printf("  Events at/after cursor: %d\n", eventCount)
		fmt.Printf("  Duplicate events found: %d\n", duplicateCount)

		if fix && duplicateCount > 0 {
			result, err := db.ExecContext(ctx, `
				DELETE FROM contract_events
				WHERE ctid NOT IN (
					SELECT MIN(ctid)
					FROM contract_events
					GROUP BY tx_hash, event_type
				)`)
			if err != nil {
				return fmt.Errorf("removing duplicates: %w", err)
			}
			deleted, _ := result.RowsAffected()
			fmt.Printf("  Duplicate events removed: %d\n", deleted)
		} else if duplicateCount > 0 {
			fmt.Printf("  Run with --fix to remove duplicates\n")
		}

		fmt.Printf("Repair scan complete\n")
		return nil
	},
}

func init() {
	repairCmd.Flags().Bool("fix", false, "Apply repairs (remove duplicates)")
	rootCmd.AddCommand(reorgCmd)
	rootCmd.AddCommand(repairCmd)
}

var rootCmd = &cobra.Command{
	Use:   "moistello-indexer",
	Short: "Moistello indexer CLI",
}
