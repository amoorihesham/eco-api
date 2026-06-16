// Command api boots the eco-api HTTP server: config, DB pool, event
// backbone, modules, and graceful shutdown.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5"

	identityhandler "github.com/amoorihesham/eco-api/internal/modules/identity/handler"
	identityrepo "github.com/amoorihesham/eco-api/internal/modules/identity/repo"
	identityservice "github.com/amoorihesham/eco-api/internal/modules/identity/service"
	"github.com/amoorihesham/eco-api/internal/modules/seller"
	sellerhandler "github.com/amoorihesham/eco-api/internal/modules/seller/handler"
	sellerrepo "github.com/amoorihesham/eco-api/internal/modules/seller/repo"
	sellerservice "github.com/amoorihesham/eco-api/internal/modules/seller/service"
	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/config"
	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/env"
	"github.com/amoorihesham/eco-api/internal/platform/events"
	"github.com/amoorihesham/eco-api/internal/platform/health"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
	applog "github.com/amoorihesham/eco-api/internal/platform/log"
)

func main() {
	err := env.Load(".env")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Fatal(err)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		_, _ = os.Stderr.WriteString("config error: " + err.Error() + "\n")
		os.Exit(1)
	}

	logger := applog.New(cfg.LogLevel, cfg.LogFormat)

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

	healthH := health.New(health.Check{Name: "postgres", Func: pool.Ping})

	// Event backbone. Modules (P3+) construct their own events.NewOutbox(pool) and register
	// bus.Subscribe(...) handlers here, before the dispatcher starts.
	bus := events.NewBus(logger)
	dispatcher := events.NewDispatcher(pool, bus, logger, cfg.OutboxPollInterval, cfg.OutboxBatchSize)

	// --- identity module (P3): auth adapters → repo → service → handler ---
	hasher := auth.NewBcryptHasher(cfg.AuthBcryptCost)
	jwt := auth.NewJWT(cfg.AuthJWTSecret, cfg.AuthAccessTTL)
	outbox := events.NewOutbox(pool)
	identitySvc := identityservice.New(pool, identityrepo.New(pool), hasher, jwt, outbox,
		identityservice.Config{RefreshTTL: cfg.AuthRefreshTTL, ResetTTL: cfg.AuthResetTTL})
	identityH := identityhandler.New(identitySvc)
	// First consumer of UserRegistered (welcome email) is wired in P16:
	//   bus.Subscribe(identity.EventUserRegistered, events.Idempotent(pool, "notification", ...))

	// --- seller module (P5): repo → service → handler; consumes identity.Reader for the apply guard ---
	sellerSvc := sellerservice.New(pool, sellerrepo.New(pool), identitySvc, outbox)
	sellerH := sellerhandler.New(sellerSvc)

	// First cross-module consumer (P5): identity reacts to SellerApproved by promoting the user's role.
	// Idempotent + at-least-once; the role flip and the processed-events mark commit in one tx.
	bus.Subscribe(seller.EventSellerApproved, events.Idempotent(pool, "identity",
		func(ctx context.Context, tx pgx.Tx, e events.Event) error {
			var p seller.ApprovedPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return err
			}
			return identitySvc.PromoteToSeller(ctx, tx, p.UserID)
		}))

	router := newRouter(logger, healthH, identityH, sellerH, jwt)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Relay committed events off the request path; drains on shutdown.
	dispatcherDone := make(chan struct{})
	go func() {
		defer close(dispatcherDone)
		logger.Info("outbox dispatcher started")
		if err := dispatcher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("dispatcher stopped with error", slog.String("error", err.Error()))
		}
	}()

	srv := &httpx.Server{
		Logger: logger,
		Cfg:    httpx.ServerConfig{Addr: cfg.HTTPPort, ReadTimeout: cfg.HTTPReadTimeout, WriteTimeout: cfg.HTTPWriteTimeout, IdleTimeout: cfg.HTTPIdleTimeout, ShutdownTimeout: cfg.HTTPShutdownTimeout},
	}

	if err := srv.Run(ctx, router); err != nil {
		logger.Error("server exited with error", slog.String("error", err.Error()))
		os.Exit(1)
	}

	<-dispatcherDone // wait for the final outbox drain
	logger.Info("shutdown complete")
}

// newRouter wires routes + middleware. Modules mount under /api/v1.
func newRouter(l *slog.Logger, h *health.Handler, identityH *identityhandler.Handler, sellerH *sellerhandler.Handler, verifier auth.Verifier) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.Live)
	mux.HandleFunc("GET /readyz", h.Ready)

	authn := auth.Authn(verifier)
	identityH.Mount(mux, authn)
	sellerH.Mount(mux, authn)

	return httpx.Chain(mux, httpx.RequestID(), httpx.Logger(l), httpx.Recoverer(l))
}
