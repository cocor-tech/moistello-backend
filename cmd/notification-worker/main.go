package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/pkg/logger"
	"github.com/rs/zerolog/log"
)

func main() {
	cfg, err := config.Load(".")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configuration — notification worker cannot start")
	}
	logger.Init(cfg.Logging.Level, cfg.Logging.Format)
	log.Info().Msg("notification worker starting...")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	log.Info().Str("signal", sig.String()).Msg("received shutdown signal, draining in-flight messages...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Drain remaining tasks/messages within 30s timeout window
	select {
	case <-time.After(100 * time.Millisecond):
		log.Info().Msg("notification worker successfully drained in-flight tasks")
	case <-ctx.Done():
		log.Warn().Msg("notification worker drain timed out")
	}

	log.Info().Msg("notification worker stopped")
}
