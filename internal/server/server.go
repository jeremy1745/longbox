package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type Server struct {
	httpServer *http.Server
	shutdownCh chan struct{}
}

func New(port int) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
		shutdownCh: make(chan struct{}),
	}
}

func (s *Server) SetHandler(handler http.Handler) {
	s.httpServer.Handler = handler
}

func (s *Server) RequestShutdown() {
	select {
	case <-s.shutdownCh:
		// already closed
	default:
		close(s.shutdownCh)
	}
}

func (s *Server) Start() error {
	// Channel to listen for shutdown signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// Channel to listen for server errors
	errCh := make(chan error, 1)

	go func() {
		slog.Info("starting server", "addr", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case sig := <-stop:
		slog.Info("shutdown signal received", "signal", sig)
	case <-s.shutdownCh:
		slog.Info("shutdown requested via API")
	}

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	slog.Info("server stopped gracefully")
	return nil
}
