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
	log.Info().Msg("webhook dispatcher starting...")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("webhook dispatcher shutting down...")
	// TODO: drain in-flight webhook deliveries and close connections here
	log.Info().Msg("webhook dispatcher stopped")
}
