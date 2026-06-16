//go:build integration

package events_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// A probe table stands in for a real module's state, so we can assert the effect happened once.
func resetProbe(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS platform_event_probe (event_id uuid PRIMARY KEY)`); err != nil {
		t.Fatalf("create probe: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE platform_event_probe`); err != nil {
		t.Fatalf("truncate probe: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE platform_outbox, platform_processed_events`); err != nil {
		t.Fatalf("truncate eventing: %v", err)
	}
}

func TestOutboxWriteIsAtomic(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetProbe(t, pool)
	ctx := context.Background()
	outbox := events.NewOutbox(pool)

	e, _ := events.NewEvent("Probe", map[string]string{"hello": "world"})

	// Rolled-back tx: neither the probe row nor the outbox row should persist.
	_ = db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO platform_event_probe (event_id) VALUES ($1)`, e.ID); err != nil {
			return err
		}
		if err := outbox.Write(ctx, tx, e); err != nil {
			return err
		}
		return errors.New("rollback")
	})

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform_outbox`).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if n != 0 {
		t.Fatalf("rollback should leave no outbox rows, got %d", n)
	}
}

func TestExactlyOnceDelivery(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetProbe(t, pool)
	ctx := context.Background()

	bus := events.NewBus(quiet())
	bus.Subscribe("Probe", events.Idempotent(pool, "probe-consumer",
		func(ctx context.Context, tx pgx.Tx, e events.Event) error {
			_, err := tx.Exec(ctx, `INSERT INTO platform_event_probe (event_id) VALUES ($1)`, e.ID)
			return err
		}))

	// Produce: state change + outbox row, atomically.
	e, _ := events.NewEvent("Probe", map[string]string{"hello": "world"})
	outbox := events.NewOutbox(pool)
	if err := db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
		return outbox.Write(ctx, tx, e)
	}); err != nil {
		t.Fatalf("produce: %v", err)
	}

	// Dispatch delivers the committed event.
	disp := events.NewDispatcher(pool, bus, quiet(), time.Second, 100)
	if err := disp.DrainOnce(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}
	// Simulate at-least-once redelivery of the same event id.
	if err := bus.Publish(ctx, e); err != nil {
		t.Fatalf("redeliver: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform_event_probe WHERE event_id = $1`, e.ID).Scan(&n); err != nil {
		t.Fatalf("count effect: %v", err)
	}
	if n != 1 {
		t.Fatalf("exactly-once: want 1 effect row, got %d", n)
	}
}
