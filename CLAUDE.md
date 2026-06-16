# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`eco-api` is a multi-vendor B2C marketplace built as a **modular monolith** in Go: one deployable binary split into independent **modules** (bounded contexts) that own their data and talk only through explicit contracts. It is a solo, learning-Go project that deliberately favors explicit, idiomatic Go (stdlib `net/http`, `pgx`, `sqlc`) over frameworks/ORMs. Read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design and [docs/PRD.md](docs/PRD.md) for the product spec; work is sequenced in phases (P0, P1, …) tracked under [docs/executions/](docs/executions/) — code comments reference these phase tags.

## Common commands

Tasks run via [Taskfile.yml](Taskfile.yml) (`task <name>`); `task` loads `.env` automatically.

| Task | What it does |
|---|---|
| `task run` | Run the API locally (`go run ./cmd/api`) |
| `task build` | Build the binary to `bin/` |
| `task test` | Unit tests, no DB (`go test ./... -count=1`) |
| `task test:integration` | DB-backed tests — starts Postgres + migrates, then `go test -tags integration ./...` |
| `task lint` | `golangci-lint run` (also checks gofmt/goimports) |
| `task generate` | `go generate ./...` then `sqlc generate` |
| `task sqlc:check` | Regenerate sqlc and fail if the committed output is stale |
| `task ci` | Full pipeline: tidy → generate → lint → test → build |
| `task db:up` / `task db:down` | Start / stop the Postgres container |
| `task migrate:up` / `migrate:down` | Apply / roll back migrations (uses `DATABASE_URL`) |
| `task migrate:new -- <name>` | Create an up/down migration pair |
| `task tools` | Install dev tools (golangci-lint, golang-migrate, sqlc) |

Run a single unit test: `go test ./internal/modules/identity/service -run TestRegister -count=1`.

**Integration tests** are gated by the `//go:build integration` tag and skip themselves when `DATABASE_URL` is unset, so plain `task test` never needs a database. They run against a **real Postgres** (not mocks).

## Module anatomy

Every module under `internal/modules/<name>/` follows the same layered shape, with **interfaces at each boundary** (see [internal/modules/identity/](internal/modules/identity/) as the reference implementation):

```
domain/    pure Go: entities, value objects, domain events, errors — imports NO infrastructure
service/   use cases; depends only on ports (interfaces) it declares in service/ports.go
repo/      adapter: implements the service's Repository port over sqlc-generated queries
handler/   net/http transport: decode → call service → encode; no business logic
port.go    the module's PUBLIC surface: events it publishes + read ports siblings may consume
```

The flow of control and the dependency rule: `handler → service → (ports) ← repo/adapters`. The `service` **defines** the interfaces it needs (`Repository`, `Outbox`, etc.); `repo/` and the `platform/` adapters satisfy them. This is what makes services unit-testable with mocks.

### Hard rules (enforced by review, lint later)

- **`domain/` and `service/` import no infrastructure SDK** — no `pgx`, `net/http`, `stripe-go`. Only `repo/` and `platform/*` adapters may.
- **Cross-module access goes through a sibling's `port.go` only** — never import another module's `service`/`repo`/`domain`, never read its tables.
- **No cross-module DB foreign keys or joins.** Tables are prefixed by owning module (`identity_users`, `order_orders`). Cross-module references store a plain UUID, resolved via ports (sync) or events (async).
- **Money and quantities are integer minor units** (cents), never floats.

## Platform layer (`internal/platform/`)

Business-agnostic infrastructure consumed as ports. Key pieces:

- **`db`** — pgx pool (`db.Open`) and `db.RunInTx(ctx, pool, func(tx) error)`, which commits on success and rolls back on error/panic. `db.Beginner` is the narrow interface services accept so a pool or a tx both work.
- **`events`** — the event backbone. `events.Event` is the immutable envelope; `Event.ID` is the idempotency key. `events.NewBus` (in-process pub/sub) + `events.NewDispatcher` (polls the outbox, relays to the bus). `events.Idempotent(pool, consumer, txHandler)` wraps a handler so it runs at most once per `(consumer, event_id)`.
- **`auth`** — JWT issue/verify (`TokenIssuer`/`Verifier`), bcrypt `Hasher`, and middleware `Authn` (verify bearer → claims in context) + `RequireRole(...)`. Use as `authn(RequireRole("admin")(handler))`.
- **`httpx`** — server lifecycle (`httpx.Run` with graceful shutdown), `Chain` for middleware, and the standard JSON/error envelope: `WriteJSON`, `WriteError`, `Unauthorized`, `Internal`, etc. Error codes are the `httpx.Code*` constants and mirror the OpenAPI spec.
- **`config`** — typed `Config` loaded from env with defaults + startup validation. **All config comes from env vars**; add new settings here.
- **`env`** — minimal `.env` loader called before config in `main.go`.

## The transactional outbox pattern (critical to get right)

Cross-module reactions are **event-driven**. A state change and its event emission are **atomic**: inside one `db.RunInTx`, the service writes its rows *and* calls `outbox.Write(ctx, tx, evt)` on the same `tx`. The dispatcher later relays committed rows to the bus; consumers dedupe by `event_id`. See `Service.Register` in [internal/modules/identity/service/service.go](internal/modules/identity/service/service.go) for the canonical example.

Repository **write** methods therefore take a `pgx.Tx` parameter (so the service composes them with the outbox write in one transaction); **read** methods take only `ctx` and return `pgx.ErrNoRows` when absent, which the service maps to domain errors. Keep this convention when adding repo methods.

Synchronous cross-module **reads** (a request can't proceed without live data — e.g. checkout reading price/stock) use narrow read-only ports exposed in `port.go`, not events.

## Database & sqlc

- **One Postgres, one schema.** Migrations are a single ordered `golang-migrate` sequence in [migrations/](migrations/), filenames module-prefixed (`000003_identity.up.sql`).
- **sqlc generates type-safe Go from SQL** ([sqlc.yaml](sqlc.yaml)). There are **three separate generation targets**, each with its own queries dir and output package:
  - `internal/platform/db/queries` → package `dbgen`
  - `internal/platform/events/queries` → package `eventsdb`
  - `internal/modules/identity/repo/queries` → package `identitydb`
- When adding a module that needs queries, add a new sqlc target (queries dir + `out` + package) and append its `out` dir to `SQLC_DIRS` in the Taskfile so `sqlc:check` covers it.
- **Workflow:** edit migrations + `queries/*.sql` → `task generate` → use generated code in `repo/`. Generated `*.sql.go`/`models.go`/`querier.go` are committed; `task sqlc:check` enforces they're current. The generated `Queries` type has `WithTx(tx)` to run on a transaction.

## Composition root

All wiring is explicit in [cmd/api/main.go](cmd/api/main.go) — no global singletons, no DI container. Adding a module means: build its adapters → repo → service → handler there, mount its routes in `newRouter`, and register any event subscribers on the `bus` **before** the dispatcher goroutine starts.
