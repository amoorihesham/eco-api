//go:build integration

package repo_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identityrepo "github.com/amoorihesham/eco-api/internal/modules/identity/repo"
	identityservice "github.com/amoorihesham/eco-api/internal/modules/identity/service"
	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
	sellerrepo "github.com/amoorihesham/eco-api/internal/modules/seller/repo"
	sellerservice "github.com/amoorihesham/eco-api/internal/modules/seller/service"
	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; run: task db:up; task migrate:up")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := db.Open(ctx, db.Config{DSN: dsn, MaxConns: 4, ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	return pool
}

func newIdentitySvc(pool *pgxpool.Pool) *identityservice.Service {
	return identityservice.New(pool, identityrepo.New(pool),
		auth.NewBcryptHasher(10),
		auth.NewJWT("test-secret-at-least-32-bytes-long!!", 15*time.Minute),
		events.NewOutbox(pool),
		identityservice.Config{RefreshTTL: time.Hour, ResetTTL: time.Hour})
}

func TestSellerLifecycleAndRoleFlip(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `TRUNCATE identity_users CASCADE`)
	_, _ = pool.Exec(ctx, `TRUNCATE seller_applications, seller_stores`)
	_, _ = pool.Exec(ctx, `TRUNCATE platform_outbox`)
	_, _ = pool.Exec(ctx, `TRUNCATE platform_processed_events`)

	identitySvc := newIdentitySvc(pool)
	sellerSvc := sellerservice.New(pool, sellerrepo.New(pool), identitySvc, events.NewOutbox(pool))

	reg, err := identitySvc.Register(ctx, "buyer@example.com", "password123", "Buyer One")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	uid := reg.User.ID

	// Apply → pending; a second apply is rejected by the one-active rule.
	app, err := sellerSvc.Apply(ctx, uid, sellerservice.ApplicationInput{StoreName: "Acme", Contact: "acme@x.com"})
	if err != nil || app.Status != domain.StatusPending {
		t.Fatalf("apply: %+v err=%v", app, err)
	}
	if _, err := sellerSvc.Apply(ctx, uid, sellerservice.ApplicationInput{StoreName: "Dup", Contact: "x"}); !errors.Is(err, domain.ErrApplicationExists) {
		t.Fatalf("want ErrApplicationExists, got %v", err)
	}

	// Approve → approved + store + exactly one SellerApproved outbox row.
	if _, err := sellerSvc.Approve(ctx, app.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	var stores, approved int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM seller_stores WHERE seller_id = $1`, uid).Scan(&stores)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM platform_outbox WHERE event_type = 'SellerApproved'`).Scan(&approved)
	if stores != 1 || approved != 1 {
		t.Fatalf("want 1 store + 1 SellerApproved, got stores=%d approved=%d", stores, approved)
	}

	// Cross-module flip: run the same consumer wired in main, twice — role becomes seller, dedupe holds.
	flip := events.Idempotent(pool, "identity", func(ctx context.Context, tx pgx.Tx, e events.Event) error {
		var p domain.SellerApprovedPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return err
		}
		return identitySvc.PromoteToSeller(ctx, tx, p.UserID)
	})
	evt := loadOutboxEvent(t, pool, ctx, "SellerApproved")
	if err := flip(ctx, evt); err != nil {
		t.Fatalf("flip 1: %v", err)
	}
	if err := flip(ctx, evt); err != nil { // replay → no-op
		t.Fatalf("flip 2 (replay): %v", err)
	}
	var role string
	_ = pool.QueryRow(ctx, `SELECT role FROM identity_users WHERE id = $1`, uid).Scan(&role)
	if role != "seller" {
		t.Fatalf("want role seller after flip, got %q", role)
	}

	// Suspend → suspended + one SellerSuspended row; store edits then blocked.
	if _, err := sellerSvc.Suspend(ctx, app.ID); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if _, err := sellerSvc.UpdateStore(ctx, uid, sellerservice.StoreInput{Name: "x", Contact: "y"}); !errors.Is(err, domain.ErrNotApproved) {
		t.Fatalf("want ErrNotApproved after suspend, got %v", err)
	}
}

func loadOutboxEvent(t *testing.T, pool *pgxpool.Pool, ctx context.Context, typ string) events.Event {
	t.Helper()
	var e events.Event
	if err := pool.QueryRow(ctx,
		`SELECT id, event_type, payload, occurred_at FROM platform_outbox WHERE event_type = $1 LIMIT 1`, typ).
		Scan(&e.ID, &e.Type, &e.Payload, &e.OccurredAt); err != nil {
		t.Fatalf("load %s: %v", typ, err)
	}
	return e
}
