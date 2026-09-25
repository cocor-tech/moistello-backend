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
		txs, err := b.poller.FetchTransactionsForLedger(ctx, seq)
		if err != nil {
			log.Warn().Err(err).Int64("ledger", seq).Msg("failed to fetch transactions for ledger in backfill")
			continue
		}

		for _, tx := range txs {
			events, err := b.poller.ExtractContractEvents(tx)
			if err != nil {
				continue
			}
			for _, evt := range events {
				if dryRun {
					log.Info().Str("event", evt.EventType).Int64("ledger", evt.Ledger).Str("txHash", evt.TxHash).Msg("[DRY-RUN] would backfill event")
					totalProcessed++
				} else {
					if err := b.processor.ProcessEvent(ctx, evt); err == nil {
						totalProcessed++
					}
				}
			}
		}
	}

	log.Info().Int("totalEvents", totalProcessed).Msg("backfill completed")
	return totalProcessed, nil
}
