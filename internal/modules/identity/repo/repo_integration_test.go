//go:build integration

package repo_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	identityrepo "github.com/amoorihesham/eco-api/internal/modules/identity/repo"
	identityservice "github.com/amoorihesham/eco-api/internal/modules/identity/service"
	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

func openTestPool(t *testing.T) *pgxpool.Pool {
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

func TestRegisterPersistsUserAndOutboxAtomically(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	// Clean slate (ON DELETE CASCADE clears child rows).
	_, _ = pool.Exec(ctx, `TRUNCATE identity_users CASCADE`)
	_, _ = pool.Exec(ctx, `TRUNCATE platform_outbox`)

	svc := identityservice.New(pool, identityrepo.New(pool),
		auth.NewBcryptHasher(10),
		auth.NewJWT("test-secret-at-least-32-bytes-long!!", 15*time.Minute),
		events.NewOutbox(pool),
		identityservice.Config{RefreshTTL: time.Hour, ResetTTL: time.Hour})

	res, err := svc.Register(ctx, "buyer@example.com", "password123", "Buyer One")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if res.AccessToken == "" || res.RefreshToken == "" {
		t.Fatal("expected tokens")
	}

	var users, outbox int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM identity_users`).Scan(&users)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM platform_outbox WHERE event_type = 'UserRegistered'`).Scan(&outbox)
	if users != 1 || outbox != 1 {
		t.Fatalf("want 1 user + 1 outbox row, got users=%d outbox=%d", users, outbox)
	}

	// Duplicate email is rejected.
	if _, err := svc.Register(ctx, "buyer@example.com", "password123", "Dup"); err == nil {
		t.Fatal("expected duplicate-email error")
	}
}
