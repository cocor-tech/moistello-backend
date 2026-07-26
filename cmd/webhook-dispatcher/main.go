package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/pkg/logger"
	"github.com/rs/zerolog/log"
)

func main() {
	cfg, err := config.Load(".")
	if err != nil {
		// log.Fatal calls os.Exit(1) internally; zerolog writes to stderr by
		// default before Init() is called, so this is safe even before Init().
		log.Fatal().Err(err).Msg("failed to load configuration — webhook dispatcher cannot start")
	}
	logger.Init(cfg.Logging.Level, cfg.Logging.Format)
	log.Info().Msg("webhook dispatcher starting...")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("webhook dispatcher shutting down...")
	// TODO: drain in-flight webhook deliveries and close connections here
	log.Info().Msg("webhook dispatcher stopped")
}
