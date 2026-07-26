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
	cfg, _ := config.Load(".")
	logger.Init(cfg.Logging.Level, cfg.Logging.Format)
	log.Info().Msg("notification worker starting...")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("notification worker shutting down...")
	// TODO: drain in-flight messages and close consumer connections here
	log.Info().Msg("notification worker stopped")
}
