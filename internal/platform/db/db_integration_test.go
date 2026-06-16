//go:build integration

package db_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/db/dbgen"
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

func TestBaselineMigrationApplied(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_extension WHERE extname = 'citext'`).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 1 {
		t.Fatal("citext extension missing — run task migrate:up")
	}
}

func TestSampleQueryGenerated(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()

	ok, err := dbgen.New(pool).DBHealthCheck(context.Background())
	if err != nil {
		t.Fatalf("DBHealthCheck: %v", err)
	}
	if ok != 1 {
		t.Fatalf("want 1, got %d", ok)
	}
}

func TestRunInTxAgainstPostgres(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS platform_tx_probe (id int PRIMARY KEY)`); err != nil {
		t.Fatalf("create probe: %v", err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS platform_tx_probe`) }()
	if _, err := pool.Exec(ctx, `TRUNCATE platform_tx_probe`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	// commit path
	if err := db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO platform_tx_probe (id) VALUES (1)`)
		return err
	}); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	// rollback path
	_ = db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
		_, _ = tx.Exec(ctx, `INSERT INTO platform_tx_probe (id) VALUES (2)`)
		return errors.New("rollback")
	})

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform_tx_probe`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("want 1 row after commit+rollback, got %d", count)
	}
}
