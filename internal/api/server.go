package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/moistello/backend/config"
)

// maxBodyBytes is the hard limit on request body size (4 MB).
// This prevents memory exhaustion from oversized request bodies (#49).
const maxBodyBytes = 4 * 1024 * 1024 // 4 MB

func RunServer(router http.Handler, cfg config.ServerConfig) error {
	// Wrap the router so every request body is capped at maxBodyBytes.
	limitedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		router.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:           fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:        limitedHandler,
		ReadTimeout:    cfg.ReadTimeout,
		WriteTimeout:   cfg.WriteTimeout,
		MaxHeaderBytes: cfg.MaxHeaderBytes,
	}

	go func() {
		log.Info().Str("addr", srv.Addr).Msg("starting API server")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down server...")

	// context.Background() is correct here: this is a top-level lifecycle context
	// for graceful server shutdown, not tied to any request.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("server forced to shutdown")
		return err
	}

	log.Info().Msg("server exited")
	return nil
}
