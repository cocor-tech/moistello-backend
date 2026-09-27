package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/payout"
	"github.com/moistello/backend/internal/domain/reputation"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/indexer"
	"github.com/moistello/backend/pkg/jobqueue"
	"github.com/moistello/backend/pkg/logger"
	"github.com/moistello/backend/pkg/postgres"
	"github.com/moistello/backend/pkg/rabbitmq"
	"github.com/moistello/backend/pkg/redis"
	"github.com/moistello/backend/pkg/stellar/soroban"
)

func main() {
	cfg, err := config.Load(".")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}
	logger.Init(cfg.Logging.Level, cfg.Logging.Format)
	log.Info().Msg("starting Moistello indexer")

	// --- Infrastructure ---

	db, err := postgres.New(cfg.Database)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer db.Close()

	redisClient, err := redis.New(cfg.Redis)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to redis")
	}
	defer redisClient.Close()

	rmqClient, err := rabbitmq.New(cfg.RabbitMQ)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to rabbitmq")
	}
	defer rmqClient.Close()

	// --- Domain repositories ---

	circleRepo := circle.NewRepository(db)
	contribRepo := contribution.NewRepository(db)
	payoutRepo := payout.NewRepository(db)
	reputationRepo := reputation.NewRepository(db)
	userRepo := user.NewRepository(db)

	// --- Indexer components ---

	cursor := indexer.NewCursorTracker(db)
	// Build contractIDs from configured contracts block (Soroban contract IDs)
	contractIDs := []string{}
	for _, v := range cfg.Contracts {
		if v != "" {
			contractIDs = append(contractIDs, v)
		}
	}
	poller := indexer.NewPoller(cfg.Stellar.HorizonURL, contractIDs)
	processor := indexer.NewEventProcessor(
		db, rmqClient,
		circleRepo, contribRepo, payoutRepo, reputationRepo, userRepo,
	)

	// Record which contract version emitted each event. Contract IDs survive an
	// upgrade, so without this an event cannot be tied back to the code that
	// produced it. The resolver is cached because it is consulted once per
	// event, and a Soroban ledger read per event would be a read per event for
	// a value that only changes on upgrade.
	processor.SetContractVersionResolver(indexer.NewCachingContractVersionResolver(
		indexer.SorobanContractVersions{
			Ledger: soroban.NewLedgerClient(cfg.Stellar.SorobanRPCURL),
		},
		indexer.DefaultContractVersionTTL,
	))

	// Wire WebSocket broadcast via Redis so API server instances
	// relay indexer events to connected clients in real time.
	processor.SetWebSocketBroadcast(func(circleID string, data any) {
		payload, err := json.Marshal(map[string]any{
			"type":     "indexer.event",
			"circleId": circleID,
			"payload":  data,
		})
		if err != nil {
			log.Warn().Err(err).Msg("indexer ws marshal")
			return
		}
		if err := redisClient.Publish(context.Background(), "moistello:ws:events", payload).Err(); err != nil {
			log.Warn().Err(err).Msg("indexer ws publish")
		}
	})

	reconciler := indexer.NewReconciler(
		cursor, poller, processor,
		indexer.NewDeduplicator(24*time.Hour),
	)

	engine := indexer.NewEngine(
		cfg.Indexer,
		db, redisClient, rmqClient,
		poller, processor, reconciler, cursor,
	)

	// --- Start the engine ---

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := engine.Start(ctx); err != nil {
		log.Fatal().Err(err).Msg("failed to start engine")
	}

	// --- Optional Job Queue Worker ---
	var jobWorker *jobqueue.Worker
	{
		jobQueue := jobqueue.NewJobQueue(db)
		jobWorker = jobqueue.NewWorker(jobQueue, 3*time.Second)
		jobWorker.Start(ctx)
	}

	log.Info().
		Str("horizon", cfg.Stellar.HorizonURL).
		Strs("contracts", contractIDs).
		Dur("poll_interval", cfg.Indexer.PollInterval).
		Int("batch_size", cfg.Indexer.BatchSize).
		Msg("indexer engine running")

	// --- Health HTTP server ---
	// Exposes /health, /health/ready, and /metrics on a separate port (default
	// 1101) so Kubernetes liveness/readiness probes and Prometheus can reach
	// the indexer without adding a dependency on the API server. Both health
	// endpoints check PostgreSQL, Redis, RabbitMQ, and cursor freshness so a
	// stuck poll loop or dropped dependency shows up as unhealthy instead of
	// the indexer always reporting ok.
	healthPort := os.Getenv("INDEXER_HEALTH_PORT")
	if healthPort == "" {
		healthPort = "1101"
	}

	healthHandler := indexer.NewHealthHandler(db, redisClient, rmqClient, cursor, cfg.Indexer.MaxCursorLag)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler.Health)
	mux.HandleFunc("/health/ready", healthHandler.Ready)
	mux.Handle("/metrics", promhttp.Handler())

	healthSrv := &http.Server{
		Addr:         ":" + healthPort,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	go func() {
		log.Info().Str("port", healthPort).Msg("indexer health server listening")
		if err := healthSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("indexer health server error")
		}
	}()

	// --- Wait for shutdown signal ---

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down indexer...")
	cancel()
	if jobWorker != nil {
		jobWorker.Stop()
	}
	engine.Stop()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = healthSrv.Shutdown(shutCtx)

	log.Info().Msg("indexer exited")
}
