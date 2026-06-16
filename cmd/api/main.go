package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/amoorihesham/eco-api/internal/platform/config"
	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/health"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
	applog "github.com/amoorihesham/eco-api/internal/platform/log"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		os.Stderr.WriteString("config error: " + err.Error() + "\n")
		os.Exit(1)
	}

	logger := applog.New(cfg.LogLevel, cfg.LogFormat)

	// Connect to Postgres before serving; fail fast if it is unreachable.
	startupCtx, cancel := context.WithTimeout(context.Background(), cfg.DBConnectTimeout)
	pool, err := db.Open(startupCtx, db.Config{
		DSN:             cfg.DatabaseURL,
		MaxConns:        cfg.DBMaxConns,
		MinConns:        cfg.DBMinConns,
		MaxConnLifetime: cfg.DBMaxConnLifetime,
		MaxConnIdleTime: cfg.DBMaxConnIdleTime,
		ConnectTimeout:  cfg.DBConnectTimeout,
	})
	cancel()
	if err != nil {
		logger.Error("database connection failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer pool.Close()

	// Readiness now reflects real DB health.
	healthH := health.New(health.Check{Name: "postgres", Func: pool.Ping})

	router := newRouter(logger, healthH)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srvCfg := httpx.ServerConfig{
		Addr:            ":" + cfg.HTTPPort,
		ReadTimeout:     cfg.HTTPReadTimeout,
		WriteTimeout:    cfg.HTTPWriteTimeout,
		IdleTimeout:     cfg.HTTPIdleTimeout,
		ShutdownTimeout: cfg.HTTPShutdownTimeout,
	}

	if err := httpx.Run(ctx, logger, srvCfg, router); err != nil {
		logger.Error("server exited with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}

// newRouter wires routes + middleware. Later phases mount their modules here (under /api/v1).
func newRouter(l *slog.Logger, h *health.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.Live)
	mux.HandleFunc("GET /readyz", h.Ready)
	return httpx.Chain(mux, httpx.RequestID(), httpx.Logger(l), httpx.Recoverer(l))
}
