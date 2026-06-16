# Execution Plan — P2: Eventing Foundation

| | |
|---|---|
| **Phase** | P2 — Eventing Foundation (see [../IMPLEMENTATION_PLAN.md](../IMPLEMENTATION_PLAN.md)) |
| **Status** | Ready to implement |
| **Date** | 2026-06-16 |
| **Outcome** | The service has an event-driven backbone: a state change and its event commit atomically (transactional outbox on the shared `RunInTx`), a background dispatcher relays committed events to an in-process bus, and consumers dedupe by `event_id` so replays produce exactly one effect. No business modules yet — only the eventing plumbing every later module reuses. |
| **Module path** | `eco-api` |

> This is an **execution document**: detailed enough to implement directly. Code blocks are working
> skeletons — type them in, adjust names to taste. Builds directly on **P1**
> ([P1-persistence-foundation.md](P1-persistence-foundation.md)). Companion docs: [PRD](../PRD.md) ·
> [ARCHITECTURE](../ARCHITECTURE.md) · [OpenAPI](../../api/openapi.yaml).

---

## 1. Overview

**Objective.** Build the event-driven backbone — **bus + transactional outbox + idempotent
delivery** — so modules can react to one another reliably from the start. P2 contains **no business
logic and no business tables**: only platform infrastructure (`internal/platform/events`, the
`platform_outbox` / `platform_processed_events` tables) and the contract every later module copies.

**In scope**
- The **event envelope** (`id`, `type`, `occurred-at`, `payload`) and the `Publisher` / `Subscriber` ports.
- An **in-process event bus** (synchronous fan-out to subscribed handlers).
- The **transactional outbox** writer, enlisted in `db.RunInTx` — the event row is written on the **same `tx`** as the state change.
- A background **dispatcher** that polls committed outbox rows, relays them to the bus, marks them dispatched, and drains on graceful shutdown.
- An **idempotency helper** — dedupe by `event_id` per consumer via `platform_processed_events`, with the dedupe mark and the handler's writes in one transaction.
- Migration `000002_platform_eventing` (the two `platform_` infra tables) + a second sqlc block for their queries.
- A **sample event + handler** exercised only by a test, proving exactly-once handling.
- Config: outbox poll interval + batch size.

**Out of scope (later phases)**
- Any real producers/consumers. First producer `UserRegistered` → **P3**; `SellerApproved`/`SellerSuspended` → **P5**; `ProductPublished`/`ProductUnpublished` → **P6**; the order/payment events → **P11/P12**.
- Any business module or business tables (identity, catalog, …) → **P3+**.
- A message **broker** (NATS/Kafka), multi-instance dispatch, retry/back-off policies, dead-letter queues → designed-for, not built (ARCHITECTURE §1 non-goals, §15). MVP is one in-process dispatcher.
- The import-boundary lint gate (services never import `pgx`/infra SDKs) is a **rule** here, **enforced** by lint in **P18**.

---

## 2. Prerequisites (Windows / PowerShell)

P0/P1 tools (Go, Docker Desktop, go-task, golangci-lint, golang-migrate, sqlc) still apply. P2 adds
**no new CLI tools** — only one Go dependency:

| Dependency | Min version | Install (PowerShell) | Verify |
|---|---|---|---|
| google/uuid | v1.6+ | `go get github.com/google/uuid` | listed in `go.mod` |

> The compose Postgres from P0 must be running for migrations and the integration tests:
> `task db:up` then `task migrate:up` (applies the new `000002` migration).

---

## 3. Tech stack & versions

| Concern | Choice |
|---|---|
| Event envelope ID | **google/uuid** (`uuid.New()`) — stable, external-safe identifiers (ARCHITECTURE §12) |
| Outbox / dedupe storage | Postgres `platform_outbox`, `platform_processed_events` (jsonb payload, `FOR UPDATE`-free single dispatcher) |
| Queries / codegen | **sqlc** — a second `sql:` block emitting into `internal/platform/events/eventsdb` |
| Atomicity | the P1 `db.RunInTx` — the outbox insert joins the caller's `tx` |
| Bus | hand-rolled in-process `map[type][]Handler`, synchronous fan-out |
| Unit-test mock | **pgxmock v4** (already present) for the dedupe path; pure-Go for the bus |

> Adds `github.com/google/uuid` (runtime). pgx/v5, pgxmock/v4, sqlc, golang-migrate are unchanged
> from P1. sqlc `overrides` map `timestamptz → time.Time` and `uuid → uuid.UUID` so the generated
> structs are clean Go (no `pgtype` in the eventing surface).

---

## 4. Target file tree (delta on P1)

```text
eco-api/
├── go.mod                                       # CHANGED: + google/uuid
├── go.sum                                       # CHANGED (go mod tidy)
├── sqlc.yaml                                    # CHANGED: + second sql block (eventsdb) with overrides
├── Taskfile.yml                                 # CHANGED: sqlc:check also diffs the events gen dir
├── .env.example                                 # CHANGED: + OUTBOX_* vars
├── cmd/api/main.go                              # CHANGED: build bus + dispatcher; run + drain on shutdown
├── internal/platform/
│   ├── config/
│   │   ├── config.go                            # CHANGED: OutboxPollInterval + OutboxBatchSize
│   │   └── config_test.go                       # CHANGED: + outbox defaults assertion
│   └── events/                                  # NEW package
│       ├── event.go                             # Event envelope, ports (Publisher/Subscriber/Handler), NewEvent
│       ├── bus.go                               # in-process Bus (Subscribe + Publish fan-out)
│       ├── outbox.go                            # Outbox writer (enlisted in tx) + Dispatcher (poll/relay/mark)
│       ├── idempotency.go                       # Idempotent(): dedupe by event_id per consumer
│       ├── queries/
│       │   └── eventing.sql                     # sqlc queries (outbox + processed-events)
│       ├── eventsdb/                            # GENERATED + committed (db.go, models.go, eventing.sql.go)
│       ├── bus_test.go                          # fan-out + handler-error (no DB)
│       └── events_integration_test.go           # //go:build integration: atomic write + exactly-once
└── migrations/
    ├── 000002_platform_eventing.up.sql          # platform_outbox + platform_processed_events
    └── 000002_platform_eventing.down.sql
```

**Import-direction rule (carried from P1):** `events` is **platform infrastructure** — it may import
`pgx`/`pgxpool`/`uuid` and the platform `db` package. From P3 onward, module `domain/` and `service/`
packages must **never** import `pgx` or `events`' internals directly; they depend on the
`Publisher`/`Subscriber` **ports** (interfaces), and the composition root injects the concrete bus +
outbox. Convention now, a lint gate in P18.

---

## 5. Execution steps

Work top to bottom; each step ends in a check. Assumes P1 is in place (`config`, `db` with
`RunInTx`, `dbgen`, migrations, sqlc, `health`, `cmd/api/main.go`).

### S1 — Add the dependency
```powershell
go get github.com/google/uuid
```
**Check:** `go.mod` lists `github.com/google/uuid`.

### S2 — Config: outbox poll interval + batch size
Edit [../../internal/platform/config/config.go](../../internal/platform/config/config.go). Add two
fields, their `Load` lines, and validation. Full additions in §8. Key additions:

```go
// struct fields
OutboxPollInterval time.Duration
OutboxBatchSize    int32

// Load()
OutboxPollInterval: envDur("OUTBOX_POLL_INTERVAL", time.Second),
OutboxBatchSize:    int32(envInt("OUTBOX_BATCH_SIZE", 100)),

// Validate()
if c.OutboxBatchSize < 1 {
    return fmt.Errorf("OUTBOX_BATCH_SIZE must be >= 1, got %d", c.OutboxBatchSize)
}
```
**Check:** `go build ./internal/platform/config`.

### S3 — Eventing migration
```powershell
task migrate:new -- platform_eventing   # creates migrations/000002_platform_eventing.{up,down}.sql
```
Fill the up/down files (full contents in §8): `platform_outbox` (id, event_type, payload jsonb,
occurred_at, dispatched_at, created_at + a partial index on undispatched rows) and
`platform_processed_events` (consumer, event_id, processed_at, PK `(consumer, event_id)`). Then:

```powershell
task db:up
task migrate:up
task migrate:version          # -> 2
```
**Check:** `migrate:version` prints `2` (not dirty); both `platform_*` tables exist.

### S4 — sqlc: second block + queries
Add the eventing `sql:` block to `sqlc.yaml` (with the `timestamptz`/`uuid` overrides) and create
`internal/platform/events/queries/eventing.sql` (full contents in §8: `InsertOutbox`,
`FetchUnsentOutbox`, `MarkOutboxDispatched`, `MarkProcessed`). Then:

```powershell
task sqlc                      # generates internal/platform/events/eventsdb/*
```
The generated `eventsdb` package is **committed** (fresh clones build without sqlc); `task sqlc:check`
guards drift for both gen dirs.

> Both sqlc blocks read the same `migrations/*.up.sql` schema, so each generated package contains
> models for all tables — harmless; queries reference only their own. `MarkProcessed` is `:execrows`
> (returns rows-affected: `1` = inserted, `0` = duplicate) — the dedupe signal.

**Check:** `go build ./internal/platform/events/eventsdb`.

### S5 — Event envelope + ports
`internal/platform/events/event.go` (full in §8): the `Event` struct, the `Handler` func type, the
`Publisher` / `Subscriber` ports, and a `NewEvent(type, payload)` constructor that assigns a fresh
`uuid` and JSON-encodes the payload.
**Check:** `go build ./internal/platform/events`.

### S6 — In-process bus
`internal/platform/events/bus.go` (full in §8): a `Bus` with `Subscribe(type, Handler)` and
`Publish(ctx, Event)` that fans out synchronously to subscribers; a handler error aborts the publish
(so the dispatcher leaves the row undispatched and retries — handlers are idempotent).
**Check:** `go build ./internal/platform/events`.

### S7 — Outbox writer + dispatcher
`internal/platform/events/outbox.go` (full in §8):
- `Outbox.Write(ctx, tx, e)` — inserts the event on the **caller's `tx`** (atomic with the state change).
- `Dispatcher.DrainOnce(ctx)` — fetch unsent rows → `bus.Publish` → `MarkOutboxDispatched`.
- `Dispatcher.Run(ctx)` — tick on the poll interval; on `ctx.Done()` do a final best-effort drain.

**Check:** `go build ./internal/platform/events`.

### S8 — Idempotency helper
`internal/platform/events/idempotency.go` (full in §8): `Idempotent(pool, consumer, txHandler)`
wraps a tx-aware handler so `MarkProcessed` (dedupe insert) and the handler's writes commit in **one**
`RunInTx`; a duplicate `event_id` (rows-affected `0`) short-circuits to a no-op.
**Check:** `go build ./internal/platform/events`.

### S9 — Wire bus + dispatcher in `main`
Edit `cmd/api/main.go` (full file in §8): after the pool, build the bus and dispatcher; run the
dispatcher in a goroutine bound to the signal context; wait for its final drain before exit. Modules
(P3+) register subscribers and construct their own `Outbox` here.
**Check:** `task run` boots; logs show the dispatcher start; `Ctrl+C` logs a clean drain + shutdown.

### S10 — Env, Taskfile, tests
Add `OUTBOX_*` to `.env.example`; extend `sqlc:check` to diff the events gen dir; write the tests
(§9).
```powershell
task test                # unit (bus fan-out) — Docker-free
task test:integration    # atomic write + exactly-once (compose Postgres)
task ci                  # tidy → sqlc generate → lint → test → build -> green
```
**Check:** `task ci` green; both test suites pass.

---

## 6. The transactional-outbox & idempotency contract

This is the discipline every later module inherits — the P2 analog of P0's response/error envelope
and P1's table-ownership rule. It is the realization of ARCHITECTURE §5 (reliability by construction),
§7 (inter-module communication), and ADR-005.

**Rules**
- **Atomic publish.** A producer writes its state change **and** the outbox row on the **same `tx`**
  (`db.RunInTx`). They commit together or not at all — no lost or phantom events.
- **At-least-once delivery.** The dispatcher relays committed rows; a crash between publish and
  `MarkOutboxDispatched` re-delivers on restart. Therefore **every handler must be idempotent**.
- **Idempotency by `event_id`.** Each consumer records processed `(consumer, event_id)` pairs in
  `platform_processed_events`; a duplicate is a no-op. The dedupe mark and the handler's effect share
  **one transaction**, so "did the work" and "marked done" are atomic.
- **Infrastructure, not business.** `platform_outbox` / `platform_processed_events` use the
  `platform_` prefix (P1 §6); on extraction each service carries its own outbox instance (§15).
- **Async by default.** Handlers run off the request path via the dispatcher. The only synchronous,
  in-transaction cross-module effects are correctness-critical invariants (stock + ledger on payment,
  ARCHITECTURE §9.1) — those are wired in P12, not here.

**The producer pattern** (established here; first *used* in P3). State + event in one `tx`:

```go
// a module service publishing atomically with its write:
err := db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
    if err := qtx.CreateUser(ctx, ...); err != nil {   // module state (sqlc on the tx)
        return err
    }
    evt, err := events.NewEvent("UserRegistered", UserRegisteredPayload{UserID: id, Email: email})
    if err != nil {
        return err
    }
    return outbox.Write(ctx, tx, evt)                   // same tx → atomic
})
```

**The consumer pattern** (idempotent reaction):

```go
bus.Subscribe("UserRegistered", events.Idempotent(pool, "notification",
    func(ctx context.Context, tx pgx.Tx, e events.Event) error {
        var p UserRegisteredPayload
        if err := json.Unmarshal(e.Payload, &p); err != nil {
            return err
        }
        // ... do the effect on tx; runs at most once per (consumer, event_id) ...
        return nil
    }))
```

| Concern | Type / helper |
|---|---|
| Build an event | `events.NewEvent("Type", payload)` → `Event{ID, Type, OccurredAt, Payload}` |
| Publish atomically | `outbox.Write(ctx, tx, e)` inside `db.RunInTx` |
| React to an event | `bus.Subscribe("Type", handler)` at wiring time |
| Idempotent handler | `events.Idempotent(pool, "<consumer>", func(ctx, tx, e) error {...})` |
| Relay loop | `events.NewDispatcher(pool, bus, log, interval, batch).Run(ctx)` |

---

## 7. Configuration reference (additions to P1)

| Env var | Type | Default | Required from |
|---|---|---|---|
| `OUTBOX_POLL_INTERVAL` | duration | `1s` | P2 |
| `OUTBOX_BATCH_SIZE` | int (≥1) | `100` | P2 |

All P0/P1 variables (`HTTP_*`, `LOG_*`, `Environment`, `DATABASE_URL`, `DB_*`) are unchanged.

---

## 8. Full file contents

**`internal/platform/config/config.go`** (additions to the P1 version — add the two fields, the two
`Load` lines, and the `Validate` guard shown in S2; nothing else changes.)

**`migrations/000002_platform_eventing.up.sql`**
```sql
-- P2 Eventing Foundation: transactional-outbox infrastructure (platform_ prefix → not business tables).

-- Events awaiting dispatch. Written in the SAME tx as the producing state change (atomic publish).
CREATE TABLE platform_outbox (
    id            uuid        PRIMARY KEY,
    event_type    text        NOT NULL,
    payload       jsonb       NOT NULL,
    occurred_at   timestamptz NOT NULL,
    dispatched_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Partial index: the dispatcher only ever scans undispatched rows, in arrival order.
CREATE INDEX idx_platform_outbox_undispatched
    ON platform_outbox (created_at)
    WHERE dispatched_at IS NULL;

-- Per-consumer dedupe ledger: makes at-least-once delivery safe (idempotent handling).
CREATE TABLE platform_processed_events (
    consumer     text        NOT NULL,
    event_id     uuid        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);
```

**`migrations/000002_platform_eventing.down.sql`**
```sql
DROP TABLE IF EXISTS platform_processed_events;
DROP TABLE IF EXISTS platform_outbox;
```

**`sqlc.yaml`** (add the second `sql:` block; keep the P1 block unchanged)
```yaml
version: "2"
sql:
  - engine: postgresql
    schema: "migrations/*.up.sql"
    queries: "internal/platform/db/queries"
    gen:
      go:
        package: "dbgen"
        out: "internal/platform/db/dbgen"
        sql_package: "pgx/v5"
        emit_interface: true
        emit_empty_slices: true
  - engine: postgresql
    schema: "migrations/*.up.sql"
    queries: "internal/platform/events/queries"
    gen:
      go:
        package: "eventsdb"
        out: "internal/platform/events/eventsdb"
        sql_package: "pgx/v5"
        emit_interface: true
        emit_empty_slices: true
        overrides:
          - db_type: "timestamptz"
            go_type: "time.Time"
          - db_type: "uuid"
            go_type: "github.com/google/uuid.UUID"
```

**`internal/platform/events/queries/eventing.sql`**
```sql
-- name: InsertOutbox :exec
INSERT INTO platform_outbox (id, event_type, payload, occurred_at)
VALUES ($1, $2, $3, $4);

-- name: FetchUnsentOutbox :many
SELECT id, event_type, payload, occurred_at
FROM platform_outbox
WHERE dispatched_at IS NULL
ORDER BY created_at
LIMIT $1;

-- name: MarkOutboxDispatched :exec
UPDATE platform_outbox
SET dispatched_at = now()
WHERE id = $1;

-- name: MarkProcessed :execrows
INSERT INTO platform_processed_events (consumer, event_id)
VALUES ($1, $2)
ON CONFLICT (consumer, event_id) DO NOTHING;
```

**`internal/platform/events/event.go`**
```go
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Event is the immutable envelope every domain event travels in.
// ID is the idempotency key: stable from creation, through the outbox and bus, to each consumer.
type Event struct {
	ID         uuid.UUID       // unique per event
	Type       string          // e.g. "UserRegistered"
	OccurredAt time.Time        // when the producing state change happened (UTC)
	Payload    json.RawMessage // module-defined, JSON-encoded
}

// Handler reacts to one event. Handlers MUST be idempotent — delivery is at-least-once.
type Handler func(ctx context.Context, e Event) error

// Publisher relays a committed event to subscribers. Implemented by the bus; called by the dispatcher.
type Publisher interface {
	Publish(ctx context.Context, e Event) error
}

// Subscriber registers handlers by event type. Modules call this at wiring time.
type Subscriber interface {
	Subscribe(eventType string, h Handler)
}

// NewEvent builds an envelope: a fresh ID, the current UTC time, and the JSON-encoded payload.
func NewEvent(eventType string, payload any) (Event, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("marshal payload for %s: %w", eventType, err)
	}
	return Event{
		ID:         uuid.New(),
		Type:       eventType,
		OccurredAt: time.Now().UTC(),
		Payload:    b,
	}, nil
}
```

**`internal/platform/events/bus.go`**
```go
package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Bus is the in-process event bus: synchronous fan-out to handlers subscribed by event type.
// It satisfies both Publisher (used by the dispatcher) and Subscriber (used by modules at wiring).
type Bus struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
	log      *slog.Logger
}

func NewBus(log *slog.Logger) *Bus {
	return &Bus{handlers: make(map[string][]Handler), log: log}
}

// Subscribe registers a handler for an event type. Call during wiring, before Run.
func (b *Bus) Subscribe(eventType string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

// Publish delivers e to every handler for its type, in registration order.
// A handler error aborts the publish so the dispatcher leaves the outbox row undispatched
// and retries on the next tick — which is safe because handlers are idempotent.
func (b *Bus) Publish(ctx context.Context, e Event) error {
	b.mu.RLock()
	hs := b.handlers[e.Type]
	b.mu.RUnlock()

	for _, h := range hs {
		if err := h(ctx, e); err != nil {
			return fmt.Errorf("handle %s (%s): %w", e.Type, e.ID, err)
		}
	}
	return nil
}
```

**`internal/platform/events/outbox.go`**
```go
package events

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"eco-api/internal/platform/events/eventsdb"
)

// Outbox writes events into platform_outbox. Producers call Write on the SAME tx as their state
// change, so the change and the event commit atomically (transactional outbox).
type Outbox struct {
	q *eventsdb.Queries
}

func NewOutbox(pool *pgxpool.Pool) *Outbox {
	return &Outbox{q: eventsdb.New(pool)}
}

// Write enlists the event insert in the caller's transaction.
func (o *Outbox) Write(ctx context.Context, tx pgx.Tx, e Event) error {
	return o.q.WithTx(tx).InsertOutbox(ctx, eventsdb.InsertOutboxParams{
		ID:         e.ID,
		EventType:  e.Type,
		Payload:    e.Payload,
		OccurredAt: e.OccurredAt,
	})
}

// Dispatcher relays committed outbox rows to the bus. One instance per process (MVP); SKIP LOCKED /
// multi-instance dispatch is a deliberate non-goal until the broker phase (ARCHITECTURE §15).
type Dispatcher struct {
	pool     *pgxpool.Pool
	q        *eventsdb.Queries
	bus      Publisher
	log      *slog.Logger
	interval time.Duration
	batch    int32
}

func NewDispatcher(pool *pgxpool.Pool, bus Publisher, log *slog.Logger, interval time.Duration, batch int32) *Dispatcher {
	return &Dispatcher{pool: pool, q: eventsdb.New(pool), bus: bus, log: log, interval: interval, batch: batch}
}

// DrainOnce dispatches one batch of undispatched events. On a publish error it stops and returns —
// the row stays undispatched and is retried next tick.
func (d *Dispatcher) DrainOnce(ctx context.Context) error {
	rows, err := d.q.FetchUnsentOutbox(ctx, d.batch)
	if err != nil {
		return fmt.Errorf("fetch outbox: %w", err)
	}
	for _, r := range rows {
		e := Event{ID: r.ID, Type: r.EventType, OccurredAt: r.OccurredAt, Payload: r.Payload}
		if err := d.bus.Publish(ctx, e); err != nil {
			return fmt.Errorf("publish %s: %w", r.ID, err)
		}
		if err := d.q.MarkOutboxDispatched(ctx, r.ID); err != nil {
			return fmt.Errorf("mark dispatched %s: %w", r.ID, err)
		}
	}
	return nil
}

// Run polls on the configured interval until ctx is cancelled, then does one final best-effort drain
// (graceful shutdown — the P0 lifecycle).
func (d *Dispatcher) Run(ctx context.Context) error {
	t := time.NewTicker(d.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := d.DrainOnce(drainCtx); err != nil {
				d.log.Error("final outbox drain failed", slog.String("error", err.Error()))
			}
			cancel()
			return ctx.Err()
		case <-t.C:
			if err := d.DrainOnce(ctx); err != nil {
				d.log.Error("outbox drain failed", slog.String("error", err.Error()))
			}
		}
	}
}
```

**`internal/platform/events/idempotency.go`**
```go
package events

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"eco-api/internal/platform/db"
	"eco-api/internal/platform/events/eventsdb"
)

// TxHandler is an effect that runs inside the dedupe transaction, on the same tx as the
// processed-events mark — so "did the work" and "marked processed" commit atomically.
type TxHandler func(ctx context.Context, tx pgx.Tx, e Event) error

// Idempotent adapts a TxHandler into a bus Handler that runs at most once per (consumer, event_id).
// MarkProcessed returns rows-affected: 1 = first time (run the effect), 0 = duplicate (no-op).
func Idempotent(pool *pgxpool.Pool, consumer string, h TxHandler) Handler {
	q := eventsdb.New(pool)
	return func(ctx context.Context, e Event) error {
		return db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
			n, err := q.WithTx(tx).MarkProcessed(ctx, eventsdb.MarkProcessedParams{
				Consumer: consumer,
				EventID:  e.ID,
			})
			if err != nil {
				return fmt.Errorf("mark processed: %w", err)
			}
			if n == 0 {
				return nil // already handled by this consumer → skip
			}
			return h(ctx, tx, e)
		})
	}
}
```

**`cmd/api/main.go`** (updated — adds the events wiring to the P1 version)
```go
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"eco-api/internal/platform/config"
	"eco-api/internal/platform/db"
	"eco-api/internal/platform/events"
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

	router := newRouter(logger, healthH)

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

	<-dispatcherDone // wait for the final outbox drain
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

**`Taskfile.yml`** — extend `sqlc:check` to guard both generated dirs:
```yaml
  sqlc:check:
    desc: Fail if generated code is stale
    cmds:
      - sqlc generate
      - git diff --exit-code -- internal/platform/db/dbgen internal/platform/events/eventsdb
```

**`.env.example`** — append:
```text
# Outbox dispatcher (P2)
OUTBOX_POLL_INTERVAL=1s
OUTBOX_BATCH_SIZE=100
```

**`go.mod`** — after `go get` + `go mod tidy`:
```text
require (
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.6.0
	github.com/pashagolub/pgxmock/v4 v4.3.0
)
```

---

## 9. Testing plan

| Test | File | Asserts | Needs DB? |
|---|---|---|---|
| Bus fan-out + handler error | `events/bus_test.go` | all subscribers receive the event; a handler error propagates from `Publish` | no |
| Config: outbox defaults | `config/config_test.go` | `OUTBOX_POLL_INTERVAL`=1s, `OUTBOX_BATCH_SIZE`=100; batch `0` → error | no |
| Atomic publish | `events/events_integration_test.go` | `RunInTx` that errors → neither state row nor outbox row persists; success → both | yes |
| Exactly-once delivery | `events/events_integration_test.go` | dispatcher delivers a written event; replaying the same event id yields exactly **one** effect | yes |

**`internal/platform/events/bus_test.go`** (no Docker)
```go
package events_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"eco-api/internal/platform/events"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestBusFansOutToAllSubscribers(t *testing.T) {
	bus := events.NewBus(quietLogger())
	var a, b int
	bus.Subscribe("Thing", func(context.Context, events.Event) error { a++; return nil })
	bus.Subscribe("Thing", func(context.Context, events.Event) error { b++; return nil })

	e, err := events.NewEvent("Thing", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	if err := bus.Publish(context.Background(), e); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if a != 1 || b != 1 {
		t.Fatalf("want both handlers called once, got a=%d b=%d", a, b)
	}
}

func TestBusPropagatesHandlerError(t *testing.T) {
	bus := events.NewBus(quietLogger())
	want := errors.New("boom")
	bus.Subscribe("Thing", func(context.Context, events.Event) error { return want })

	e, _ := events.NewEvent("Thing", nil)
	if err := bus.Publish(context.Background(), e); !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
}
```

**`internal/platform/events/events_integration_test.go`** (build-tagged; against compose Postgres)
```go
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

	"eco-api/internal/platform/db"
	"eco-api/internal/platform/events"
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
```

Run: `task test` (unit) and `task test:integration` (DB-backed).

---

## 10. Definition of Done

- [ ] `go get github.com/google/uuid` done; `go.mod` lists it.
- [ ] `task migrate:up` applies cleanly; `task migrate:version` → `2` (not dirty); `platform_outbox` and `platform_processed_events` exist.
- [ ] `task sqlc` generates `internal/platform/events/eventsdb/*`; `task sqlc:check` reports no diff for either gen dir.
- [ ] `task run` boots; logs show `outbox dispatcher started`; `Ctrl+C` logs the final drain then `shutdown complete`.
- [ ] `task test` (unit) green — bus fan-out + handler-error + config defaults.
- [ ] `task test:integration` green — a rolled-back producer leaves no outbox row; a delivered-then-replayed event yields exactly **one** effect.
- [ ] `task ci` green (tidy → sqlc generate → lint → test → build).
- [ ] Repo matches the §4 tree; only `platform/*` imports `pgx`/`uuid`; the `platform_` prefix holds for both new tables.

*Demo: an integration test proving exactly-once handling — a state change and its event commit
atomically, the dispatcher delivers, and replaying the same event produces exactly one effect.*

---

## 11. Verification (PowerShell)

```powershell
# 1. Migrate + generate
task db:up
task migrate:up
task migrate:version          # -> 2
task sqlc

# 2. Build pipeline
task ci                       # tidy, sqlc generate, lint, test, build -> green

# 3. Run + observe the dispatcher lifecycle
task run                      # logs: "outbox dispatcher started", then "http server listening"
#   Ctrl+C  -> logs the final drain, then "shutdown complete"

# 4. The headline guarantee
task test:integration         # atomic outbox write + exactly-once delivery

# 5. Tables exist with the platform_ prefix
docker compose exec postgres psql -U eco -d eco -c "\dt platform_*"
#   -> platform_outbox, platform_processed_events
```

---

## 12. Handoff to P3 (Identity & Auth)

P3 is the first **business module** and the first **real producer** — it plugs into the P2 seams:
- **Module template:** `domain / service / repo / handler / port` (ARCHITECTURE §5). The service
  declares its `Repository` **and** a `Publisher` port; the composition root injects the `Outbox`-backed
  publisher and the bus.
- **First event:** `identity` publishes `UserRegistered` by calling `outbox.Write(ctx, tx, evt)` on the
  **same `tx`** as the `identity_users` insert (the §6 producer pattern).
- **First tables:** `identity_users`, `identity_addresses` (the `identity_` prefix; a third migration).
- **Auth platform:** `internal/platform/auth` (JWT issue/verify, password hashing, RBAC middleware) —
  ports per ARCHITECTURE §6/§10.
- **First consumer (P16/stretch):** `notification` subscribes to `UserRegistered` via
  `events.Idempotent(pool, "notification", ...)` — the §6 consumer pattern.
