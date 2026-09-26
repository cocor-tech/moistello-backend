package indexer

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
)

// BackfillJob rescans a ledger range and processes missed events idempotently.
type BackfillJob struct {
	poller    *Poller
	processor *EventProcessor
}

func NewBackfillJob(poller *Poller, processor *EventProcessor) *BackfillJob {
	return &BackfillJob{poller: poller, processor: processor}
}

func (b *BackfillJob) Run(ctx context.Context, fromLedger, toLedger int64, dryRun bool) (int, error) {
	if fromLedger > toLedger {
		return 0, fmt.Errorf("fromLedger (%d) cannot be greater than toLedger (%d)", fromLedger, toLedger)
	}

	log.Info().Int64("fromLedger", fromLedger).Int64("toLedger", toLedger).Bool("dryRun", dryRun).Msg("starting indexer backfill")

	totalProcessed := 0
	for seq := fromLedger; seq <= toLedger; seq++ {
		txs, err := b.poller.FetchTransactions(ctx, seq)
		if err != nil {
			log.Warn().Err(err).Int64("ledger", seq).Msg("failed to fetch transactions for ledger in backfill")
			continue
		}

		for _, tx := range b.poller.FilterByContract(txs) {
			if dryRun {
				log.Info().Int64("ledger", tx.Ledger).Str("txHash", tx.Hash).Msg("[DRY-RUN] would backfill transaction")
				totalProcessed++
				continue
			}
			tx := tx
			if err := b.processor.ProcessTransaction(ctx, &tx); err == nil {
				totalProcessed++
			}
		}
	}

	log.Info().Int("totalTransactions", totalProcessed).Msg("backfill completed")
	return totalProcessed, nil
}
