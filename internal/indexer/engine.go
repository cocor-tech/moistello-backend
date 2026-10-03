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

// defaultReorgWindow is how many ledgers behind the cursor the reorg check
// inspects when indexer.reorg_window is not configured (#346).
const defaultReorgWindow = 10

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
	// history records the hashes of recently indexed ledgers so a reorg can be
	// detected, and chain applies the resulting rollback (#346).
	history *LedgerHistory
	chain   *ChainReconciler
	rewind  Rewinder
	// deadLetters records events that could not be processed so the failure is
	// recoverable rather than silently dropped (#349).
	deadLetters DeadLetterStore
	wg          sync.WaitGroup
	stopCh      chan struct{}
	metrics     *IndexerMetrics
	watchdog    *StallWatchdog
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
		processor.versionUnknown = metrics.ContractVersionUnknown
		processor.SetEventCounters(metrics.Events)
		if poller != nil {
			processor.SetKnownContracts(poller.ContractIDs())
		}
	}

	window := cfg.ReorgWindow
	if window <= 0 {
		window = defaultReorgWindow
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
		history:     NewLedgerHistory(window),
		chain:       &ChainReconciler{Window: window},
		rewind:      NewPostgresRewinder(db, cursor),
		deadLetters: NewDeadLetterStore(db),
		stopCh:      make(chan struct{}),
		metrics:     metrics,
		watchdog:    NewStallWatchdog(cfg.StallThreshold, nil),
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
	e.wg.Add(1)
	go func() { defer e.wg.Done(); e.watchdog.Run(ctx, e.stopCh) }()

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

// checkReorg looks for a reorganization behind the cursor and, when one is
// found inside the configured window, deletes the events derived from the
// abandoned branch and rewinds the cursor so the replacement ledgers are
// replayed from the fork point (#346). It returns the cursor the poll loop
// should continue from, which differs from the input only after a rewind.
//
// An error is returned when the rollback itself fails, or when the reorg
// reaches past the window: the indexer cannot pick a safe fork point in that
// case and continuing would compound the inconsistency, so the poll cycle
// fails loudly instead.
func (e *Engine) checkReorg(ctx context.Context, cursor *Cursor) (*Cursor, error) {
	if e.chain == nil || e.history == nil || e.rewind == nil || cursor.LastLedger <= 0 {
		return cursor, nil
	}

	plan, err := e.chain.DetectReorg(ctx, e.poller, e.history, cursor.LastLedger)
	if err != nil {
		return nil, fmt.Errorf("reorg check: %w", err)
	}
	if plan == nil {
		return cursor, nil
	}

	deleted, err := e.rewind.DeleteEventsFrom(ctx, plan.ForkLedger)
	if err != nil {
		return nil, fmt.Errorf("reorg rollback: %w", err)
	}
	if err := e.rewind.Rewind(ctx, plan.LastLedger); err != nil {
		return nil, fmt.Errorf("reorg rollback: %w", err)
	}

	// Forget the abandoned branch's hashes so replayed ledgers are recorded
	// afresh instead of comparing against the branch they replaced.
	e.history.Truncate(plan.ForkLedger)

	if e.metrics != nil && e.metrics.ReorgsDetected != nil {
		e.metrics.ReorgsDetected.Inc()
	}

	log.Warn().
		Int64("forkLedger", plan.ForkLedger).
		Int64("rewoundTo", plan.LastLedger).
		Int64("eventsDeleted", deleted).
		Msg("reorg rollback complete — replaying from fork point")

	return &Cursor{Chain: cursor.Chain, LastLedger: plan.LastLedger, LastProcessedAt: time.Now()}, nil
}

// DeadLetter is called when an event cannot be processed. Recording the
// failure is what makes it recoverable: without it the cursor advances past
// the ledger, the deduplicator already holds the hash, and the event is lost
// with no trace (#349). A nil store disables the behaviour.
func (e *Engine) deadLetter(ctx context.Context, txn *Transaction, cause error) {
	if e.deadLetters == nil {
		return
	}

	payload, err := MarshalPayload(txn)
	if err != nil {
		// A payload we cannot serialise must not stop the failure being
		// recorded; the hash and error are the parts that matter for triage.
		log.Warn().Err(err).Str("hash", txn.Hash).Msg("serializing dead letter payload")
	}

	entry := &DeadLetterEntry{
		Chain:    "stellar",
		TxHash:   txn.Hash,
		Ledger:   txn.Ledger,
		Error:    cause.Error(),
		Attempts: 1,
		Payload:  payload,
		Status:   "dead_letter",
	}

	if _, err := e.deadLetters.Record(ctx, entry); err != nil {
		// Never let bookkeeping turn a recoverable per-event failure into a
		// failed poll cycle: log loudly and carry on.
		log.Error().Err(err).Str("hash", txn.Hash).Msg("recording dead letter failed")
		return
	}

	if e.metrics != nil && e.metrics.DeadLettered != nil {
		e.metrics.DeadLettered.Inc()
	}
}

func (e *Engine) poll(ctx context.Context) error {
	cursor, err := e.cursor.GetCurrent(ctx)
	if err != nil {
		return fmt.Errorf("reading cursor: %w", err)
	}
	e.metrics.CursorLagSeconds.Set(cursor.Lag(time.Now()).Seconds())

	// Reorg check first: if a previously indexed branch was abandoned, the
	// cursor has to be rolled back before any new ledger is processed,
	// otherwise the abandoned and replacement events both end up persisted.
	cursor, err = e.checkReorg(ctx, cursor)
	if err != nil {
		return err
	}

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
		// Record the branch this ledger belongs to so a later reorg that
		// replaces it can be detected (#346).
		if e.history != nil {
			e.history.Record(ledger.Sequence, ledger.Hash)
		}

		txns, err := e.poller.FetchTransactions(ctx, ledger.Sequence)
		if err != nil {
			log.Warn().Err(err).Int64("ledger", ledger.Sequence).Msg("skipping ledger")
			continue
		}

		filtered := e.poller.FilterByContract(txns)
		for i := range filtered {
			// Attach the ledger's close time so the processor can use it
			// instead of time.Now() for consistent, ledger-ordered timestamps (#471).
			filtered[i].LedgerCloseTime = ledger.ClosedAt
			txn := &filtered[i]
			if e.dedup.Has(txn.Hash) {
				continue
			}
			e.dedup.Add(txn.Hash)

			if err := e.processor.ProcessTransaction(ctx, txn); err != nil {
				log.Error().Err(err).Str("hash", txn.Hash).Msg("processing failed")
				e.metrics.ProcessErrors.Inc()
				e.deadLetter(ctx, txn, err)
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
		e.watchdog.Processed()
	}

	e.metrics.EventsProcessed.Add(float64(processed))
	log.Debug().Int("ledgers", len(ledgers)).Int("processed", processed).Msg("poll cycle complete")
	return nil
}
