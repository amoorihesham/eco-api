# Execution Plan — P1: Persistence Foundation

| | |
|---|---|
| **Phase** | P1 — Persistence Foundation (see [../IMPLEMENTATION_PLAN.md](../IMPLEMENTATION_PLAN.md)) |
| **Status** | Ready to implement |
| **Date** | 2026-06-16 |
| **Outcome** | The service connects to Postgres on boot, applies migrations, and queries it type-safely. A shared `RunInTx` unit-of-work exists, `/readyz` reflects real DB health, and the `<module>_<table>` ownership discipline is locked in before the first table. |
| **Module path** | `eco-api` |

> This is an **execution document**: detailed enough to implement directly. Code blocks are working
> skeletons — type them in, adjust names to taste. Builds directly on **P0**
> ([P0-project-bootstrapping.md](P0-project-bootstrapping.md)). Companion docs: [PRD](../PRD.md) ·
> [ARCHITECTURE](../ARCHITECTURE.md) · [OpenAPI](../../api/openapi.yaml).

---

## 1. Overview

**Objective.** Give the service a real database it can connect to, migrate, and query type-safely — and
lock in the shared-schema discipline before the first table exists. P1 contains **no business logic and
no module tables** — only persistence plumbing and the conventions every later module inherits.

**In scope**
- A `pgx` connection pool (`internal/platform/db`), wired into config and the readiness probe.
- `RunInTx` — the shared transaction helper (the unit-of-work the outbox and every service reuse).
- Migration tooling (`golang-migrate` CLI + Taskfile) with a **baseline** migration (extensions + conventions).
- The type-safe query workflow (`sqlc`), proven by a sample query.
- The **`<module>_<table>` prefix / table-ownership convention**, documented in `migrations/`.
- Config: `DATABASE_URL` becomes **required**; pool tuning is configurable.
- `/readyz` completed with a real `postgres` check.

**Out of scope (later phases)**
- Event bus / transactional outbox / processed-events tables → **P2**.
- Any business module or module tables (identity, catalog, …) → **P3+**.
- Embedded-FS migration runner, auto-migrate-on-boot, and **testcontainers** integration harness →
  deferred to **P18** (CI/hardening). Migrations run as an **explicit step** per ARCHITECTURE §14.
- The import-boundary lint gate (services never import `pgx`) is *introduced as a rule here*, **enforced**
  by lint in **P18**.

---

## 2. Prerequisites (Windows / PowerShell)

P0's tools (Go, Docker Desktop, go-task, golangci-lint) still apply. P1 adds two **CLI tools** (installed
into `$(go env GOPATH)\bin`, already on `PATH` from P0):

| Tool | Min version | Install (PowerShell) | Verify |
|---|---|---|---|
| golang-migrate | v4 | `go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest` | `migrate -version` |
| sqlc | v1.25+ | `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest` | `sqlc version` |

> The `-tags 'postgres'` build tag is **required** — without it the `migrate` binary has no Postgres driver.
> Both are installed by `task tools` (§8). The compose Postgres from P0 must be running for migrations/tests:
> `task db:up`.

---

## 3. Tech stack & versions

| Concern | Choice |
|---|---|
| DB driver / pool | **pgx v5** (`github.com/jackc/pgx/v5` + `pgxpool`) |
| Queries / codegen | **sqlc** (v2 config, `sql_package: pgx/v5`) → type-safe Go, no ORM |
| Migrations | **golang-migrate** CLI (ordered, explicit `migrate up`) |
| Transactions | `db.RunInTx` over a small `Beginner` interface (pgx) |
| Unit-test mock | **pgxmock v4** (`github.com/pashagolub/pgxmock/v4`) — DB-less `RunInTx` tests |
| Local DB | `postgres:16-alpine` (the P0 compose service) |

> P0's "zero external dependencies" no longer holds: P1 adds `pgx/v5` (runtime) and `pgxmock/v4` (test).
> `go.sum` now exists — expected. `golang-migrate` and `sqlc` are **tools**, not module dependencies.

---

## 4. Target file tree (delta on P0)

```text
eco-api/
├── go.mod                                  # CHANGED: + pgx/v5, + pgxmock/v4 (test)
├── go.sum                                  # NEW (from go mod tidy)
├── sqlc.yaml                               # NEW: codegen config
├── Taskfile.yml                            # CHANGED: migrate:*, sqlc, test:integration, tools, ci
├── Dockerfile                              # CHANGED: COPY go.sum for build cache
├── .env.example                            # CHANGED: + DB pool vars
├── cmd/api/main.go                         # CHANGED: open pool, register DB check, defer Close
├── internal/platform/
│   ├── config/
│   │   ├── config.go                       # CHANGED: DATABASE_URL required + DB pool fields
│   │   └── config_test.go                  # NEW: required-URL + defaults
│   └── db/                                 # NEW package
│       ├── db.go                           # Config + Open(ctx) → *pgxpool.Pool
│       ├── tx.go                           # Beginner + RunInTx (the unit of work)
│       ├── queries/
│       │   └── system.sql                  # sqlc sample query
│       ├── dbgen/                          # GENERATED + committed (db.go, models.go, system.sql.go)
│       ├── tx_test.go                      # RunInTx unit test (pgxmock, no Docker)
│       └── db_integration_test.go          # //go:build integration (compose Postgres)
└── migrations/
    ├── 000001_baseline.up.sql              # extensions + convention header
    ├── 000001_baseline.down.sql
    └── README.md                           # ownership + no-cross-FK rules
```

**Import-direction rule (carried from P0, extended):** `db` and the generated `dbgen` may import
`pgx`/`pgxpool` — they are **platform infrastructure** (just as `httpx` imports `net/http`). From P3 onward,
`domain/` and `service/` packages must **never** import `pgx`; only `platform/*` adapters and module `repo/`
packages may. This is convention now, a lint gate in P18.

---

## 5. Execution steps

Work top to bottom; each step ends in a check. Assumes P0 is in place (`config`, `log`, `httpx`, `health`,
`cmd/api/main.go`, Taskfile, compose).

### S1 — Add the dependency & tools
```powershell
go get github.com/jackc/pgx/v5
go get github.com/pashagolub/pgxmock/v4
task tools          # installs golang-migrate (+postgres tag) and sqlc
```
**Check:** `migrate -version` and `sqlc version` both print; `go.mod` lists `pgx/v5`.

### S2 — Config: require `DATABASE_URL` + pool tuning
Edit the existing [../../internal/platform/config/config.go](../../internal/platform/config/config.go).
Add the DB fields, an `envInt` helper, the `Load` lines, and the validation. Full file in §8. The key
additions:

```go
// struct fields
DatabaseURL       string
DBMaxConns        int32
DBMinConns        int32
DBMaxConnLifetime time.Duration
DBMaxConnIdleTime time.Duration
DBConnectTimeout  time.Duration

// Load()
DatabaseURL:       env("DATABASE_URL", ""),
DBMaxConns:        int32(envInt("DB_MAX_CONNS", 10)),
DBMinConns:        int32(envInt("DB_MIN_CONNS", 2)),
DBMaxConnLifetime: envDur("DB_MAX_CONN_LIFETIME", time.Hour),
DBMaxConnIdleTime: envDur("DB_MAX_CONN_IDLE_TIME", 30*time.Minute),
DBConnectTimeout:  envDur("DB_CONNECT_TIMEOUT", 5*time.Second),

// Validate()
if strings.TrimSpace(c.DatabaseURL) == "" {
    return fmt.Errorf("DATABASE_URL is required")
}
if c.DBMaxConns < 1 {
    return fmt.Errorf("DB_MAX_CONNS must be >= 1, got %d", c.DBMaxConns)
}
if c.DBMinConns < 0 || c.DBMinConns > c.DBMaxConns {
    return fmt.Errorf("DB_MIN_CONNS must be 0..DB_MAX_CONNS (%d), got %d", c.DBMaxConns, c.DBMinConns)
}
```

> Note: the live `config.go` uses `Environment` (`development|staging|production`) — P1 **keeps** that and
> only **adds** DB config. We do not rename P0's fields.

**Check:** `go build ./internal/platform/config`.

### S3 — DB pool
`internal/platform/db/db.go`:

```go
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds the DSN and pool tuning.
type Config struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	ConnectTimeout  time.Duration
}

// Open parses the DSN, applies pool settings, verifies connectivity, and returns a ready pool.
// The caller owns the pool and must Close() it.
func Open(ctx context.Context, c Config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(c.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if c.MaxConns > 0 {
		pc.MaxConns = c.MaxConns
	}
	if c.MinConns > 0 {
		pc.MinConns = c.MinConns
	}
	if c.MaxConnLifetime > 0 {
		pc.MaxConnLifetime = c.MaxConnLifetime
	}
	if c.MaxConnIdleTime > 0 {
		pc.MaxConnIdleTime = c.MaxConnIdleTime
	}

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, c.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}
```
**Check:** `go build ./internal/platform/db`.

### S4 — `RunInTx` (the unit of work)
`internal/platform/db/tx.go`:

```go
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Beginner starts a transaction. *pgxpool.Pool satisfies it; pgx.Tx does too (nested → savepoints).
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// RunInTx runs fn inside one transaction: commit on success, rollback on error or panic.
// This is the unit of work that services and the outbox writer (P2) share — every write issued on the
// passed tx commits atomically, or none does.
func RunInTx(ctx context.Context, db Beginner, fn func(tx pgx.Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
```

Inside `fn`, callers run sqlc queries on the tx (`dbgen.New(tx)` or `q.WithTx(tx)`); from P2, the outbox
insert joins the **same** `tx`. **Check:** `go build ./internal/platform/db`.

### S5 — Baseline migration
```powershell
task migrate:new -- baseline      # creates migrations/000001_baseline.{up,down}.sql
```
Fill `000001_baseline.up.sql` (full contents in §8): a convention header comment + `CREATE EXTENSION IF
NOT EXISTS pgcrypto` and `citext`. `000001_baseline.down.sql` drops both.

```powershell
task db:up
task migrate:up
task migrate:version              # -> 1
```
**Check:** `migrate:version` prints `1` (not dirty).

### S6 — sqlc workflow
`sqlc.yaml` (repo root) and `internal/platform/db/queries/system.sql` (full contents in §8). Then:

```powershell
task sqlc                         # generates internal/platform/db/dbgen/*
```
This proves codegen end-to-end: the sample `DBHealthCheck` query becomes a typed Go method. The generated
`dbgen` package is **committed** (so a fresh clone builds without sqlc installed); `task sqlc:check` guards
against drift.

> **Schema source:** sqlc reads DDL from `migrations/*.up.sql` (a glob — it deliberately skips `*.down.sql`,
> whose `DROP`s would otherwise confuse type inference). Each module from P3 adds its own `sql:` block
> emitting into its `repo/` (see §6). If a sqlc version rejects the glob, list the up-files explicitly.

**Check:** `go build ./internal/platform/db/dbgen`.

### S7 — Wire the pool + readiness check in `main`
Edit `cmd/api/main.go` (full file in §8): open the pool right after the logger, `defer pool.Close()`, and
register the DB readiness check:

```go
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
```
`pool.Ping` already has the `func(context.Context) error` shape `health.Check.Func` expects.
**Check:** `task run`, then `Invoke-RestMethod http://localhost:8080/readyz` → `status: ok`, `checks.postgres: ok`.

### S8 — Env, Taskfile, Dockerfile
Add DB pool vars to `.env.example`; add the `migrate:*`, `sqlc`, `sqlc:check`, `test:integration` tasks and
extend `tools` + `ci`; update the Dockerfile to `COPY go.mod go.sum ./` (all in §8). Copy env if needed:
```powershell
Copy-Item .env.example .env -Force   # only if you want the new vars; DATABASE_URL already present from P0
```
**Check:** `task ci` is green (tidy → sqlc generate → lint → test → build).

### S9 — Tests
Write the three tests (§9). Unit tests need no DB; the integration test needs `task db:up` + `task migrate:up`.
```powershell
task test                # unit only (fast, Docker-free)
task test:integration    # DB-backed (compose Postgres)
```
**Check:** both green.

---

## 6. The shared-schema & table-ownership convention

This is the discipline every later module inherits — the P1 analog of P0's response/error envelope. It is
the realization of ARCHITECTURE §8 and is what keeps modules independently extractable.

**Rules**
- **One database, one schema.** Boundaries are *logical*, enforced by convention + review.
- **Every table is prefixed by its owning module:** `identity_users`, `catalog_products`, `order_suborders`.
  Platform infrastructure uses `platform_` (`platform_outbox`, `platform_processed_events` — P2).
- **No foreign keys or joins across module prefixes.** A cross-module reference stores the foreign **ID**
  only — a plain `uuid` column, never an FK (e.g. `order_items.product_id` is a bare UUID).
- **FKs are allowed *within* a module** (`order_suborders.order_id → order_orders.id`).
- **Money & quantities** are integer **minor units** (cents), never floats.
- **Migrations** are one ordered sequence; filenames are module-prefixed for clarity.

This is documented in [migrations/README.md](../../migrations/README.md) so it lives next to the SQL.

**The Repository-port convention** (the contract P1 establishes; first *used* in P3). The **service**
declares the interface it needs; the **repo** implements it by wrapping sqlc-generated queries; `RunInTx`
supplies the tx for atomic writes:

```go
// service package — declares only what it needs (a port):
type Repository interface {
	CreateUser(ctx context.Context, u domain.User) error
	GetUserByEmail(ctx context.Context, email string) (domain.User, error)
}

// repo package — adapter wrapping sqlc, satisfies the port:
type Repo struct{ q *identitydb.Queries }
func New(pool *pgxpool.Pool) *Repo { return &Repo{q: identitydb.New(pool)} }

// an atomic write (state + event) uses RunInTx; the tx flows into sqlc and (P2) the outbox:
err := db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
	qtx := r.q.WithTx(tx)
	// qtx.CreateUser(ctx, ...)        // module write
	// outbox.Write(ctx, tx, evt)      // P2: same tx → atomic
	return nil
})
```

| Concern | Type / helper |
|---|---|
| Open a pool | `db.Open(ctx, db.Config{DSN: cfg.DatabaseURL, ...})` |
| Unit of work | `db.RunInTx(ctx, pool, func(tx pgx.Tx) error { ... })` |
| Bind queries to a tx | `q.WithTx(tx)` (sqlc-generated) |
| New query set | `dbgen.New(pool)` or `dbgen.New(tx)` |
| Readiness | `health.New(health.Check{Name: "postgres", Func: pool.Ping})` |

---

## 7. Configuration reference (additions to P0)

| Env var | Type | Default | Required from |
|---|---|---|---|
| `DATABASE_URL` | string (DSN) | — | **P1** (was optional in P0) |
| `DB_MAX_CONNS` | int (≥1) | `10` | P1 |
| `DB_MIN_CONNS` | int (0..max) | `2` | P1 |
| `DB_MAX_CONN_LIFETIME` | duration | `1h` | P1 |
| `DB_MAX_CONN_IDLE_TIME` | duration | `30m` | P1 |
| `DB_CONNECT_TIMEOUT` | duration | `5s` | P1 |

All P0 variables (`HTTP_*`, `LOG_*`, `Environment`) are unchanged.

---

## 8. Full file contents

**`internal/platform/config/config.go`** (updated — replaces the P0 version)
```go
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment string

	HTTPPort            string
	HTTPReadTimeout     time.Duration
	HTTPWriteTimeout    time.Duration
	HTTPIdleTimeout     time.Duration
	HTTPShutdownTimeout time.Duration

	LogLevel  string
	LogFormat string

	DatabaseURL       string
	DBMaxConns        int32
	DBMinConns        int32
	DBMaxConnLifetime time.Duration
	DBMaxConnIdleTime time.Duration
	DBConnectTimeout  time.Duration
}

func Load() (Config, error) {
	c := Config{
		Environment:         env("Environment", "development"),
		HTTPPort:            env("HTTP_PORT", "8080"),
		LogLevel:            env("LOG_LEVEL", "info"),
		LogFormat:           env("LOG_FORMAT", "json"),
		HTTPReadTimeout:     envDur("HTTP_READ_TIMEOUT", 5*time.Second),
		HTTPWriteTimeout:    envDur("HTTP_WRITE_TIMEOUT", 10*time.Second),
		HTTPIdleTimeout:     envDur("HTTP_IDLE_TIMEOUT", 120*time.Second),
		HTTPShutdownTimeout: envDur("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),

		DatabaseURL:       env("DATABASE_URL", ""),
		DBMaxConns:        int32(envInt("DB_MAX_CONNS", 10)),
		DBMinConns:        int32(envInt("DB_MIN_CONNS", 2)),
		DBMaxConnLifetime: envDur("DB_MAX_CONN_LIFETIME", time.Hour),
		DBMaxConnIdleTime: envDur("DB_MAX_CONN_IDLE_TIME", 30*time.Minute),
		DBConnectTimeout:  envDur("DB_CONNECT_TIMEOUT", 5*time.Second),
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) Validate() error {
	if !oneOf(c.Environment, "development", "staging", "production") {
		return fmt.Errorf("Environment must be one of: development, staging, production, got %s", c.Environment)
	}
	if !oneOf(c.LogLevel, "debug", "info", "warn", "error") {
		return fmt.Errorf("LOG_LEVEL invalid: %q", c.LogLevel)
	}
	if !oneOf(c.LogFormat, "json", "text") {
		return fmt.Errorf("LOG_FORMAT invalid: %q", c.LogFormat)
	}
	if p, err := strconv.Atoi(c.HTTPPort); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("HTTP_PORT invalid: %q", c.HTTPPort)
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.DBMaxConns < 1 {
		return fmt.Errorf("DB_MAX_CONNS must be >= 1, got %d", c.DBMaxConns)
	}
	if c.DBMinConns < 0 || c.DBMinConns > c.DBMaxConns {
		return fmt.Errorf("DB_MIN_CONNS must be 0..DB_MAX_CONNS (%d), got %d", c.DBMaxConns, c.DBMinConns)
	}
	return nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if strings.EqualFold(v, a) {
			return true
		}
	}
	return false
}
```

**`cmd/api/main.go`** (updated — replaces the P0 version)
```go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"eco-api/internal/platform/config"
	"eco-api/internal/platform/db"
	"eco-api/internal/platform/health"
	"eco-api/internal/platform/httpx"
	applog "eco-api/internal/platform/log"
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
```

**`sqlc.yaml`**
```yaml
version: "2"
sql:
  - engine: postgresql
    schema: "migrations/*.up.sql"          # glob: read only up-migrations (skip *.down.sql DROPs)
    queries: "internal/platform/db/queries"
    gen:
      go:
        package: "dbgen"
        out: "internal/platform/db/dbgen"
        sql_package: "pgx/v5"
        emit_interface: true               # generates Querier (handy for mocking)
        emit_empty_slices: true
```

**`internal/platform/db/queries/system.sql`**
```sql
-- name: DBHealthCheck :one
SELECT 1::int AS ok;
```

**`internal/platform/db/dbgen/system.sql.go`** — *generated by `task sqlc`; do not hand-edit.* For
reference, the sample produces:
```go
// Code generated by sqlc. DO NOT EDIT.
const dbHealthCheck = `-- name: DBHealthCheck :one
SELECT 1::int AS ok`

func (q *Queries) DBHealthCheck(ctx context.Context) (int32, error) {
	row := q.db.QueryRow(ctx, dbHealthCheck)
	var ok int32
	err := row.Scan(&ok)
	return ok, err
}
```

**`migrations/000001_baseline.up.sql`**
```sql
-- Baseline migration: database-wide conventions and shared extensions.
--
-- TABLE OWNERSHIP
--   Every table is prefixed by its owning module: <module>_<table>
--   (identity_users, catalog_products, order_suborders, ...).
--   Platform infrastructure uses the platform_ prefix (platform_outbox — added in P2).
--
-- CROSS-MODULE RULE
--   No foreign keys or joins across module prefixes. A cross-module reference stores the foreign
--   ID only — a plain uuid column, never an FK. FKs are allowed only WITHIN a module.

CREATE EXTENSION IF NOT EXISTS pgcrypto;   -- gen_random_uuid() + crypto helpers
CREATE EXTENSION IF NOT EXISTS citext;     -- case-insensitive text (email columns)
```

**`migrations/000001_baseline.down.sql`**
```sql
DROP EXTENSION IF EXISTS citext;
DROP EXTENSION IF EXISTS pgcrypto;
```

**`migrations/README.md`**
```markdown
# Migrations

`golang-migrate`, one ordered sequence. Create with `task migrate:new -- <name>`, apply with
`task migrate:up`. Filenames: `<seq>_<name>.{up,down}.sql` (prefix the name by module, e.g. `catalog_products`).

## Table-ownership convention
- Every table is prefixed by its owning module: `identity_users`, `catalog_products`, `order_suborders`.
  Platform infrastructure uses `platform_` (e.g. `platform_outbox`, P2).
- **No foreign keys or joins across module prefixes.** Cross-module references store the foreign **ID**
  only — a plain `uuid`, never an FK (e.g. `order_items.product_id`).
- FKs are allowed **within** a module (`order_suborders.order_id → order_orders.id`).
- Money and quantities are integer **minor units** (cents), never floats.

These rules keep modules independently extractable (ARCHITECTURE §8, §15).
```

**`Taskfile.yml`** — add these tasks and update `tools` + `ci`:
```yaml
  # --- database & codegen (P1) ---
  migrate:new:
    desc: "Create a migration pair: task migrate:new -- <name>"
    cmds:
      - migrate create -ext sql -dir migrations -seq {{.CLI_ARGS}}

  migrate:up:
    desc: Apply all up migrations
    cmds:
      - migrate -path migrations -database "$DATABASE_URL" up

  migrate:down:
    desc: Roll back the last migration
    cmds:
      - migrate -path migrations -database "$DATABASE_URL" down 1

  migrate:version:
    desc: Print the current migration version
    cmds:
      - migrate -path migrations -database "$DATABASE_URL" version

  migrate:force:
    desc: "Force a version (clear a dirty state): task migrate:force -- <version>"
    cmds:
      - migrate -path migrations -database "$DATABASE_URL" force {{.CLI_ARGS}}

  sqlc:
    desc: Generate type-safe Go from SQL
    cmds:
      - sqlc generate

  sqlc:check:
    desc: Fail if generated code is stale
    cmds:
      - sqlc generate
      - git diff --exit-code -- internal/platform/db/dbgen

  test:integration:
    desc: Run DB-backed integration tests (needs task db:up + task migrate:up)
    cmds:
      - go test -tags integration ./... -count=1

  # --- updated ---
  tools:
    desc: Install dev tools
    cmds:
      - go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
      - go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
      - go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

  ci:
    desc: Full local pipeline (tidy, generate, lint, test, build)
    cmds:
      - go mod tidy
      - sqlc generate
      - golangci-lint run
      - go test ./... -count=1
      - go build -o {{.APP_BIN}} ./cmd/api
```
> `$DATABASE_URL` resolves from `.env` because the P0 Taskfile already declares `dotenv: ['.env']`; go-task
> injects it into each command's environment.

**`.env.example`** — append:
```text
# Database pool (P1)
DB_MAX_CONNS=10
DB_MIN_CONNS=2
DB_MAX_CONN_LIFETIME=1h
DB_MAX_CONN_IDLE_TIME=30m
DB_CONNECT_TIMEOUT=5s
```
(`DATABASE_URL` is already present from P0.)

**`Dockerfile`** — update the cache layer now that `go.sum` exists:
```dockerfile
COPY go.mod go.sum ./
RUN go mod download
```

**`go.mod`** — after `go get` + `go mod tidy`:
```text
module eco-api

go 1.24

require (
	github.com/jackc/pgx/v5 v5.6.0
	github.com/pashagolub/pgxmock/v4 v4.3.0
)
```
> Exact patch versions resolve via `go get`/`go mod tidy`, which also adds indirect deps and writes `go.sum`.

---

## 9. Testing plan

| Test | File | Asserts | Needs DB? |
|---|---|---|---|
| Config: `DATABASE_URL` required + defaults | `config/config_test.go` | empty URL → error; DB pool defaults applied | no |
| `RunInTx` commit/rollback | `db/tx_test.go` | success → `Commit`; error → `Rollback` (pgxmock) | no |
| Migrations + sqlc + tx (integration) | `db/db_integration_test.go` | citext present; sample query → `1`; commit persists, rollback discards | yes |

**`internal/platform/config/config_test.go`**
```go
package config

import (
	"testing"
	"time"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when DATABASE_URL is empty")
	}
}

func TestLoadDBDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://eco:ecopass@localhost:5432/eco?sslmode=disable")

	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.DBMaxConns != 10 {
		t.Errorf("DBMaxConns: want 10, got %d", c.DBMaxConns)
	}
	if c.DBMinConns != 2 {
		t.Errorf("DBMinConns: want 2, got %d", c.DBMinConns)
	}
	if c.DBConnectTimeout != 5*time.Second {
		t.Errorf("DBConnectTimeout: want 5s, got %s", c.DBConnectTimeout)
	}
}
```

**`internal/platform/db/tx_test.go`** (pgxmock — no Docker)
```go
package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"eco-api/internal/platform/db"
)

func TestRunInTxCommitsOnSuccess(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("new mock: %v", err)
	}
	defer mock.Close()

	mock.ExpectBegin()
	mock.ExpectCommit()

	if err := db.RunInTx(context.Background(), mock, func(tx pgx.Tx) error { return nil }); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRunInTxRollsBackOnError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("new mock: %v", err)
	}
	defer mock.Close()

	want := errors.New("boom")
	mock.ExpectBegin()
	mock.ExpectRollback()

	if err := db.RunInTx(context.Background(), mock, func(tx pgx.Tx) error { return want }); !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
```

**`internal/platform/db/db_integration_test.go`** (build-tagged; against compose Postgres)
```go
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

	"eco-api/internal/platform/db"
	"eco-api/internal/platform/db/dbgen"
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
```

Run: `task test` (unit) and `task test:integration` (DB-backed).

---

## 10. Definition of Done

- [ ] `go get`/`task tools` done; `migrate -version` and `sqlc version` resolve.
- [ ] `task migrate:up` applies cleanly; `task migrate:version` → `1` (not dirty).
- [ ] `task sqlc` generates `internal/platform/db/dbgen/*`; `task sqlc:check` reports no diff.
- [ ] `task run` boots; logs show DB connect then `http server listening`.
- [ ] `GET /readyz` → `200` with `{"status":"ok","checks":{"postgres":"ok"}}`.
- [ ] Stopping Postgres (`task db:down`) makes `GET /readyz` → `503` with `postgres` error.
- [ ] Missing `DATABASE_URL` makes the process exit non-zero with `DATABASE_URL is required`.
- [ ] `task test` (unit) green; `task test:integration` green with the DB up.
- [ ] `task ci` green (tidy → sqlc generate → lint → test → build).
- [ ] Repo matches the §4 tree; `db`/`dbgen` may import pgx, nothing else does; convention in `migrations/README.md`.

*Demo: `/readyz` green against compose Postgres; pull the DB → `/readyz` flips to 503.*

---

## 11. Verification (PowerShell)

```powershell
# 1. Bring up the DB, migrate, generate
task db:up
task migrate:up
task migrate:version          # -> 1
task sqlc

# 2. Build pipeline
task ci                       # tidy, sqlc generate, lint, test, build -> green

# 3. Run + probe (second terminal, after `task run`)
Invoke-RestMethod http://localhost:8080/readyz    # -> status: ok ; checks.postgres: ok
Invoke-RestMethod http://localhost:8080/healthz   # -> status: ok (unchanged from P0)

# 4. Readiness reflects real dependency health
task db:down
Invoke-RestMethod http://localhost:8080/readyz    # -> 503 (postgres check fails)
task db:up

# 5. Config fail-fast
$saved = $env:DATABASE_URL; $env:DATABASE_URL = ""
go run ./cmd/api              # exits non-zero: "DATABASE_URL is required"
$env:DATABASE_URL = $saved

# 6. DB-backed tests
task test:integration         # migrations + sample query + RunInTx commit/rollback
```

---

## 12. Handoff to P2 (Eventing Foundation)

P2 plugs into the seams P1 created — no rework:
- **Migration:** add `00000X_platform_eventing.up.sql` creating `platform_outbox` and
  `platform_processed_events` (the `platform_` prefix — infrastructure, not business tables).
- **Outbox writer enlisted in `RunInTx`:** the writer inserts the event row on the **same `tx`** as the
  state change, so the state change and the event commit atomically.
- **Dispatcher:** a background loop polls committed outbox rows and relays them to the in-process bus;
  drains on graceful shutdown (the P0 lifecycle).
- **Idempotency:** consumers dedupe by `event_id` via `platform_processed_events`.
- **sqlc:** add a second `sql:` block (or queries dir) for the outbox/processed-events queries, emitting
  into the eventing package — following the §6 codegen pattern.
- **Repository ports:** the interface-at-the-repo-boundary pattern from §6 is what the first real module
  (P3) copies.
```
