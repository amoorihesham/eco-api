//go:build integration

package repo_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	identityrepo "github.com/amoorihesham/eco-api/internal/modules/identity/repo"
	identityservice "github.com/amoorihesham/eco-api/internal/modules/identity/service"
	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

func newAccountService(t *testing.T, pool *pgxpool.Pool) *identityservice.Service {
	t.Helper()
	return identityservice.New(pool, identityrepo.New(pool),
		auth.NewBcryptHasher(10),
		auth.NewJWT("test-secret-at-least-32-bytes-long!!", 15*time.Minute),
		events.NewOutbox(pool),
		identityservice.Config{RefreshTTL: time.Hour, ResetTTL: time.Hour})
}

func openAccountPool(t *testing.T) *pgxpool.Pool {
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

func addr(def bool) identityservice.AddressInput {
	return identityservice.AddressInput{
		Recipient: "Buyer One", Line1: "1 Main St", City: "Cairo",
		PostalCode: "11511", Country: "EG", IsDefault: def,
	}
}

func TestAddressBookAndDefaultInvariant(t *testing.T) {
	pool := openAccountPool(t)
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `TRUNCATE identity_users CASCADE`)

	svc := newAccountService(t, pool)
	reg, err := svc.Register(ctx, "buyer@example.com", "password123", "Buyer One")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	uid := reg.User.ID

	// First address is forced default.
	a1, err := svc.CreateAddress(ctx, uid, addr(false))
	if err != nil {
		t.Fatalf("create a1: %v", err)
	}
	if !a1.IsDefault {
		t.Fatal("first address must be default")
	}

	// Second address requesting default demotes the first.
	a2, err := svc.CreateAddress(ctx, uid, addr(true))
	if err != nil {
		t.Fatalf("create a2: %v", err)
	}
	assertSingleDefault(t, pool, ctx, uid, a2.ID)

	// Deleting the default promotes the newest remaining (a1).
	if err := svc.DeleteAddress(ctx, uid, a2.ID); err != nil {
		t.Fatalf("delete a2: %v", err)
	}
	assertSingleDefault(t, pool, ctx, uid, a1.ID)

	// Profile update round-trips.
	u, err := svc.UpdateProfile(ctx, uid, "Renamed Buyer")
	if err != nil || u.Name != "Renamed Buyer" {
		t.Fatalf("update profile: %+v err=%v", u, err)
	}
}

func TestAddressOwnershipIsolation(t *testing.T) {
	pool := openAccountPool(t)
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `TRUNCATE identity_users CASCADE`)

	svc := newAccountService(t, pool)
	a, _ := svc.Register(ctx, "a@example.com", "password123", "A")
	b, _ := svc.Register(ctx, "b@example.com", "password123", "B")

	owned, err := svc.CreateAddress(ctx, a.User.ID, addr(false))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// User B cannot read user A's address — it is indistinguishable from "missing".
	if _, err := svc.GetAddress(ctx, b.User.ID, owned.ID); !errors.Is(err, domain.ErrAddressNotFound) {
		t.Fatalf("want ErrAddressNotFound for cross-user access, got %v", err)
	}
	// User B cannot delete it either.
	if err := svc.DeleteAddress(ctx, b.User.ID, owned.ID); !errors.Is(err, domain.ErrAddressNotFound) {
		t.Fatalf("want ErrAddressNotFound for cross-user delete, got %v", err)
	}
}

func assertSingleDefault(t *testing.T, pool *pgxpool.Pool, ctx context.Context, uid, want uuid.UUID) {
	t.Helper()
	var count int
	var defaultID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM identity_addresses WHERE user_id = $1 AND is_default`, uid).Scan(&count); err != nil {
		t.Fatalf("count defaults: %v", err)
	}
	if count != 1 {
		t.Fatalf("want exactly 1 default, got %d", count)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM identity_addresses WHERE user_id = $1 AND is_default`, uid).Scan(&defaultID); err != nil {
		t.Fatalf("read default id: %v", err)
	}
	if defaultID != want {
		t.Fatalf("default is %v, want %v", defaultID, want)
	}
}
