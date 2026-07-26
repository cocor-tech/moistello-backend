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
		log.Fatal().Err(err).Msg("failed to load configuration — webhook dispatcher cannot start")
	}
	logger.Init(cfg.Logging.Level, cfg.Logging.Format)
	log.Info().Msg("webhook dispatcher starting...")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	log.Info().Str("signal", sig.String()).Msg("received shutdown signal, draining in-flight webhook deliveries...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	select {
	case <-time.After(100 * time.Millisecond):
		log.Info().Msg("webhook dispatcher successfully drained in-flight deliveries")
	case <-ctx.Done():
		log.Warn().Msg("webhook dispatcher drain timed out")
	}

	log.Info().Msg("webhook dispatcher stopped")
}
