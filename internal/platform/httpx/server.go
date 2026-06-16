package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// ServerConfig holds the listen address and timeouts.
type ServerConfig struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

type Server struct {
	Logger *slog.Logger
	Cfg    ServerConfig
}

// Run starts an HTTP server with handler and blocks until ctx is canceled,
// then gracefully shuts it down within cfg.ShutdownTimeout.
func (s *Server) Run(ctx context.Context, handler http.Handler) error {
	srv := &http.Server{
		Addr:         s.Cfg.Addr,
		Handler:      handler,
		ReadTimeout:  s.Cfg.ReadTimeout,
		WriteTimeout: s.Cfg.WriteTimeout,
		IdleTimeout:  s.Cfg.IdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		s.Logger.Info("HTTP Server listening", slog.String("address", s.Cfg.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.Logger.Info("Shutdown signal recived")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.Cfg.ShutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
