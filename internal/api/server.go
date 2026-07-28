package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/pkg/tracing"
	"github.com/rs/zerolog/log"
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

	if !cfg.TLSEnabled {
		log.Warn().Str("addr", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)).Msg("TLS is not enabled — all traffic including authentication tokens is transmitted in plaintext")
	}

	srv := &http.Server{
		Addr:           fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:        limitedHandler,
		ReadTimeout:    cfg.ReadTimeout,
		WriteTimeout:   cfg.WriteTimeout,
		MaxHeaderBytes: cfg.MaxHeaderBytes,
	}

	go func() {
		log.Info().Str("addr", srv.Addr).Msg("starting API server")
		var err error
		if cfg.TLSEnabled {
			if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" {
				log.Fatal().Msg("TLS enabled but TLS certificate or key path not provided")
			}
			err = srv.ListenAndServeTLS(cfg.TLSCertPath, cfg.TLSKeyPath)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	var redirectSrv *http.Server
	if cfg.TLSEnabled && cfg.HTTPRedirectPort > 0 {
		redirectAddr := fmt.Sprintf("%s:%d", cfg.Host, cfg.HTTPRedirectPort)
		redirectSrv = &http.Server{
			Addr:         redirectAddr,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target := fmt.Sprintf("https://%s%s", r.Host, r.RequestURI)
				http.Redirect(w, r, target, http.StatusMovedPermanently)
			}),
		}
		go func() {
			log.Info().Str("addr", redirectAddr).Msg("starting HTTP-to-HTTPS redirect server")
			if err := redirectSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error().Err(err).Msg("HTTP redirect server failed")
			}
		}()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	log.Info().Str("signal", sig.String()).Msg("received shutdown signal, stopping new connections and draining in-flight requests...")

	// Stop accepting new keep-alive connections
	srv.SetKeepAlivesEnabled(false)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if redirectSrv != nil {
		_ = redirectSrv.Shutdown(ctx)
	}

	if err := srv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("server forced to shutdown due to timeout or error")
		return err
	}

	// Shutdown OpenTelemetry tracer provider
	log.Info().Msg("shutting down OpenTelemetry tracer provider...")
	if err := tracing.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("failed to shutdown tracer provider")
	}

	log.Info().Msg("server gracefully shut down with all in-flight requests drained")
	return nil
}
