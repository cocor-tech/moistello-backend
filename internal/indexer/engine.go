package indexer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/pkg/rabbitmq"
)

// Engine is the main indexer orchestrator. It polls Horizon for new ledgers,
// processes matching transactions, broadcasts via WebSocket, publishes events
// to RabbitMQ, and periodically reconciles state.
type Engine struct {
	cfg         config.IndexerConfig
	db          *sqlx.DB
	redisClient *redis.Client
	rmqClient   *rabbitmq.Client
	cursor      *CursorTracker
	poller      *Poller
	processor   *EventProcessor
	reconciler  *Reconciler
	dedup       *Deduplicator
	wg          sync.WaitGroup
	stopCh      chan struct{}
	metrics     *IndexerMetrics
}

// NewEngine creates a new Engine with the given dependencies.
func NewEngine(
	cfg config.IndexerConfig,
	db *sqlx.DB,
	redisClient *redis.Client,
	rmqClient *rabbitmq.Client,
	poller *Poller,
	processor *EventProcessor,
	reconciler *Reconciler,
	cursor *CursorTracker,
) *Engine {
	metrics := NewIndexerMetrics()
	if processor != nil {
		processor.unknownEvents = metrics.UnknownContractEvents
		if poller != nil {
			processor.SetKnownContracts(poller.ContractIDs())
		}
	}
	return &Engine{
		cfg:         cfg,
		db:          db,
		redisClient: redisClient,
		rmqClient:   rmqClient,
		cursor:      cursor,
		poller:      poller,
		processor:   processor,
		reconciler:  reconciler,
		dedup:       NewDeduplicator(24 * time.Hour),
		stopCh:      make(chan struct{}),
		metrics:     metrics,
	}
}

// Start begins the indexer event loop. It launches the reconciler, dedup
// pruner, and poll loop as background goroutines.
func (e *Engine) Start(ctx context.Context) error {
	log.Info().Msg("starting indexer engine")

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.reconciler.StartReconciliation(ctx, e.cfg.PollInterval*10)
	}()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.dedup.StartPruning(ctx, 1*time.Hour)
	}()

	e.wg.Add(1)
	go e.runPollLoop(ctx)

	return nil
}

// Stop gracefully shuts down the indexer, waiting for all goroutines to finish.
func (e *Engine) Stop() {
	log.Info().Msg("stopping indexer engine")
	close(e.stopCh)
	e.wg.Wait()
	log.Info().Msg("indexer stopped and all engine goroutines drained")
}

// Metrics returns the engine's Prometheus metrics.
func (e *Engine) Metrics() *IndexerMetrics { return e.metrics }

func (e *Engine) runPollLoop(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(e.cfg.PollInterval)
	defer ticker.Stop()
	log.Info().Dur("interval", e.cfg.PollInterval).Msg("poll loop started")

	for {
		select {
		case <-ticker.C:
			if err := e.poll(ctx); err != nil {
				log.Error().Err(err).Msg("poll cycle failed")
				e.metrics.PollErrors.Inc()
			}
		case <-e.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (e *Engine) poll(ctx context.Context) error {
	cursor, err := e.cursor.GetCurrent(ctx)
	if err != nil {
		return fmt.Errorf("reading cursor: %w", err)
	}
	e.metrics.CursorLagSeconds.Set(cursor.Lag(time.Now()).Seconds())

	ledgers, err := e.poller.FetchLedgers(ctx, cursor.LastLedger, e.cfg.BatchSize)
	if err != nil {
		e.metrics.PollErrors.Inc()
		return fmt.Errorf("fetching ledgers: %w", err)
	}

	if len(ledgers) == 0 {
		return nil
	}

	processed := 0
	for _, ledger := range ledgers {
		txns, err := e.poller.FetchTransactions(ctx, ledger.Sequence)
		if err != nil {
			log.Warn().Err(err).Int64("ledger", ledger.Sequence).Msg("skipping ledger")
			continue
		}

		filtered := e.poller.FilterByContract(txns)
		for _, txn := range filtered {
			if e.dedup.Has(txn.Hash) {
				continue
			}
			e.dedup.Add(txn.Hash)

			if err := e.processor.ProcessTransaction(ctx, &txn); err != nil {
				log.Error().Err(err).Str("hash", txn.Hash).Msg("processing failed")
				e.metrics.ProcessErrors.Inc()
				continue
			}
			processed++
		}
	}

	if len(ledgers) > 0 {
		lastLedger := ledgers[len(ledgers)-1].Sequence
		if err := e.cursor.Update(ctx, lastLedger); err != nil {
			return fmt.Errorf("updating cursor: %w", err)
		}
		e.metrics.LastLedger.Set(float64(lastLedger))
	}

	e.metrics.EventsProcessed.Add(float64(processed))
	log.Debug().Int("ledgers", len(ledgers)).Int("processed", processed).Msg("poll cycle complete")
	return nil
}
