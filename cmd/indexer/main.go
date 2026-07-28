package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/domain/circle"
	"github.com/moistello/backend/internal/domain/contribution"
	"github.com/moistello/backend/internal/domain/payout"
	"github.com/moistello/backend/internal/domain/reputation"
	"github.com/moistello/backend/internal/domain/user"
	"github.com/moistello/backend/internal/indexer"
	"github.com/moistello/backend/pkg/logger"
	"github.com/moistello/backend/pkg/postgres"
	"github.com/moistello/backend/pkg/rabbitmq"
	"github.com/moistello/backend/pkg/redis"
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
	_ = user.NewRepository(db) // wired for future account auto-creation

	// --- Indexer components ---

	cursor := indexer.NewCursorTracker(db)
	contractIDs := []string{cfg.Stellar.MasterPublicKey}
	poller := indexer.NewPoller(cfg.Stellar.HorizonURL, contractIDs)
	processor := indexer.NewEventProcessor(
		db, rmqClient,
		circleRepo, contribRepo, payoutRepo, reputationRepo,
	)

	// Wire WebSocket broadcast via Redis so API server instances
	// relay indexer events to connected clients in real time.
	processor.SetWebSocketBroadcast(func(circleID string, data any) {
		payload, err := json.Marshal(map[string]any{
			"type":    "indexer.event",
			"circleId": circleID,
			"payload": data,
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
		indexer.NewDeduplicator(24 * time.Hour),
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

	log.Info().
		Str("horizon", cfg.Stellar.HorizonURL).
		Strs("contracts", contractIDs).
		Dur("poll_interval", cfg.Indexer.PollInterval).
		Int("batch_size", cfg.Indexer.BatchSize).
		Msg("indexer engine running")

	// --- Health HTTP server ---
	// Exposes /health and /health/ready on a separate port (default 1101) so
	// Kubernetes liveness and readiness probes can reach the indexer without
	// adding a dependency on the API server.
	healthPort := os.Getenv("INDEXER_HEALTH_PORT")
	if healthPort == "" {
		healthPort = "1101"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"status":"ok","service":"indexer"}`)
	})
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		alive := rmqClient.IsAlive()
		if !alive {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, `{"status":"not ready","error":"rabbitmq unreachable"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"status":"ready","service":"indexer"}`)
	})

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
	engine.Stop()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = healthSrv.Shutdown(shutCtx)

	log.Info().Msg("indexer exited")
}
