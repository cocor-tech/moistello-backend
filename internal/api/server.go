package api

import (
	"context"
	"errors"
	"fmt"
	"net"
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

// defaultShutdownTimeout bounds the drain when server.shutdown_timeout is unset.
const defaultShutdownTimeout = 30 * time.Second

// ErrDrainDeadlineExceeded is returned by Shutdown when in-flight requests
// did not finish inside the configured timeout and connections were closed
// forcibly.
var ErrDrainDeadlineExceeded = errors.New("shutdown: drain deadline exceeded, connections closed forcibly")

// ShutdownHooks lets main wire the pieces that must be stopped in a specific
// order when the process receives SIGTERM.
type ShutdownHooks struct {
	// PreDrain runs as soon as the shutdown signal arrives, before the
	// listener stops accepting connections. Use it to fail readiness probes
	// so load balancers route new traffic to other replicas.
	PreDrain []func()
	// Drain runs after in-flight HTTP requests have completed (or the drain
	// deadline passed). Long-lived connections such as WebSockets and
	// background loops are stopped here; each hook must honour ctx.
	Drain []func(context.Context)
	// CloseLast runs after everything else: database, cache and message
	// queue pools, which in-flight work may still need until this point.
	CloseLast []func()
}

// Server owns the HTTP listener(s) and orchestrates graceful shutdown.
type Server struct {
	cfg        config.ServerConfig
	hooks      ShutdownHooks
	srv        *http.Server
	redirect   *http.Server
	ln         net.Listener
	redirectLn net.Listener
	serveErr   chan error
}

// NewServer wraps router with the request body cap and prepares the
// listeners described by cfg. Nothing is bound until Start is called.
func NewServer(router http.Handler, cfg config.ServerConfig, hooks ShutdownHooks) *Server {
	limitedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		router.ServeHTTP(w, r)
	})

	s := &Server{
		cfg:   cfg,
		hooks: hooks,
		srv: &http.Server{
			Addr:           fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			Handler:        limitedHandler,
			ReadTimeout:    cfg.ReadTimeout,
			WriteTimeout:   cfg.WriteTimeout,
			MaxHeaderBytes: cfg.MaxHeaderBytes,
		},
		serveErr: make(chan error, 2),
	}

	if cfg.TLSEnabled && cfg.HTTPRedirectPort > 0 {
		s.redirect = &http.Server{
			Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.HTTPRedirectPort),
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target := fmt.Sprintf("https://%s%s", r.Host, r.RequestURI)
				http.Redirect(w, r, target, http.StatusMovedPermanently)
			}),
		}
	}
	return s
}

// Start binds the listener(s) and begins serving in the background. Serve
// failures other than a clean close are delivered on Err.
func (s *Server) Start() error {
	if s.cfg.TLSEnabled && (s.cfg.TLSCertPath == "" || s.cfg.TLSKeyPath == "") {
		return errors.New("TLS enabled but TLS certificate or key path not provided")
	}
	if !s.cfg.TLSEnabled {
		log.Warn().Str("addr", s.srv.Addr).Msg("TLS is not enabled — all traffic including authentication tokens is transmitted in plaintext")
	}

	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.srv.Addr, err)
	}
	s.ln = ln

	go func() {
		log.Info().Str("addr", ln.Addr().String()).Msg("starting API server")
		var serveErr error
		if s.cfg.TLSEnabled {
			serveErr = s.srv.ServeTLS(ln, s.cfg.TLSCertPath, s.cfg.TLSKeyPath)
		} else {
			serveErr = s.srv.Serve(ln)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.serveErr <- fmt.Errorf("server failed: %w", serveErr)
		}
	}()

	if s.redirect != nil {
		rln, err := net.Listen("tcp", s.redirect.Addr)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("listening on %s: %w", s.redirect.Addr, err)
		}
		s.redirectLn = rln
		go func() {
			log.Info().Str("addr", rln.Addr().String()).Msg("starting HTTP-to-HTTPS redirect server")
			if err := s.redirect.Serve(rln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error().Err(err).Msg("HTTP redirect server failed")
			}
		}()
	}
	return nil
}

// Addr returns the bound address of the main listener, useful when the
// configured port is 0.
func (s *Server) Addr() string {
	if s.ln == nil {
		return s.srv.Addr
	}
	return s.ln.Addr().String()
}

// Err delivers serve failures that happen after Start returned.
func (s *Server) Err() <-chan error { return s.serveErr }

// Shutdown performs the ordered graceful shutdown:
//
//  1. PreDrain hooks (readiness flips to not-ready),
//  2. stop accepting connections and disable keep-alives,
//  3. wait for in-flight requests until ctx expires, then force-close,
//  4. Drain hooks (WebSocket hub, bridges, background loops),
//  5. CloseLast hooks (database, cache, queue pools).
//
// Every stage runs even when an earlier one timed out, so pools are always
// closed. ErrDrainDeadlineExceeded is returned when requests were cut off.
func (s *Server) Shutdown(ctx context.Context) error {
	start := time.Now()
	for _, hook := range s.hooks.PreDrain {
		hook()
	}

	// Stop accepting new connections and tell keep-alive clients to reconnect elsewhere.
	s.srv.SetKeepAlivesEnabled(false)

	if s.redirect != nil {
		_ = s.redirect.Shutdown(ctx)
	}

	var result error
	if err := s.srv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Dur("waited", time.Since(start)).Msg("in-flight requests did not drain in time; forcing connections closed")
		_ = s.srv.Close()
		result = ErrDrainDeadlineExceeded
	} else {
		log.Info().Dur("drained_in", time.Since(start)).Msg("all in-flight requests drained")
	}

	for _, hook := range s.hooks.Drain {
		hook(ctx)
	}

	for _, hook := range s.hooks.CloseLast {
		hook()
	}

	log.Info().Dur("total", time.Since(start)).Bool("forced", result != nil).Msg("server shut down")
	return result
}

// RunServer keeps the historical entry point: the callbacks are treated as
// Drain hooks. New code should use RunServerWithHooks so pools close last.
func RunServer(router http.Handler, cfg config.ServerConfig, onShutdown ...func(ctx context.Context)) error {
	return RunServerWithHooks(router, cfg, ShutdownHooks{Drain: onShutdown})
}

// RunServerWithHooks starts the server, blocks until SIGINT/SIGTERM (or a
// serve failure), then drains within cfg.ShutdownTimeout. Connections still
// open when the timeout elapses are closed forcibly.
func RunServerWithHooks(router http.Handler, cfg config.ServerConfig, hooks ShutdownHooks) error {
	hooks.Drain = append(hooks.Drain, func(ctx context.Context) {
		log.Info().Msg("shutting down OpenTelemetry tracer provider...")
		if err := tracing.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("failed to shutdown tracer provider")
		}
	})

	server := NewServer(router, cfg, hooks)
	if err := server.Start(); err != nil {
		return err
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case sig := <-quit:
		log.Info().Str("signal", sig.String()).Dur("timeout", shutdownTimeout(cfg)).
			Msg("received shutdown signal, stopping new connections and draining in-flight requests...")
	case err := <-server.Err():
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout(cfg))
	defer cancel()
	return server.Shutdown(ctx)
}

func shutdownTimeout(cfg config.ServerConfig) time.Duration {
	if cfg.ShutdownTimeout <= 0 {
		return defaultShutdownTimeout
	}
	return cfg.ShutdownTimeout
}
