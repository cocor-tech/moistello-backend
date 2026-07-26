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
		log.Fatal().Err(err).Msg("failed to load configuration — notification worker cannot start")
	}
	logger.Init(cfg.Logging.Level, cfg.Logging.Format)
	log.Info().Msg("notification worker starting...")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("notification worker shutting down...")
	// TODO: drain in-flight messages and close consumer connections here
	log.Info().Msg("notification worker stopped")
}
