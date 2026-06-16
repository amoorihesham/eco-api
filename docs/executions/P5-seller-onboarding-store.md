# Execution Plan — P5: Seller Onboarding & Store

| | |
|---|---|
| **Phase** | P5 — Seller Onboarding & Store (see [../IMPLEMENTATION_PLAN.md](../IMPLEMENTATION_PLAN.md)) |
| **Status** | Implemented |
| **Date** | 2026-06-16 |
| **Outcome** | The first **brand-new module since P3** ships (`internal/modules/seller`). A buyer submits a seller application (`POST /api/v1/seller/applications`); an admin approves it (`POST /api/v1/admin/sellers/{id}/approve`), which atomically flips the application to `approved`, creates the store, and publishes `SellerApproved`; **identity consumes that event** and promotes the user's role to `seller` (the project's first cross-module event consumer). The approved seller then reads/edits their store (`GET`/`PATCH /api/v1/seller/store`); a `suspend` flips status to `suspended`, publishes `SellerSuspended`, and blocks store edits (403) while leaving the role intact. |
| **Module path** | `github.com/amoorihesham/eco-api` |

> This is an **execution document**: detailed enough to implement directly. Code blocks are working
> skeletons — type them in, adjust names to taste. Builds on **P4** ([P4-account.md](P4-account.md)) and
> is the **first module to copy the P3 template wholesale** ([P3-identity-auth.md](P3-identity-auth.md)).
> Companion docs: [PRD](../PRD.md) · [ARCHITECTURE](../ARCHITECTURE.md) · [OpenAPI](../../api/openapi.yaml).

---

## 1. Overview

**Objective.** Turn buyers into sellers through an **admin-gated workflow**, and with it establish two
reusable patterns: **"admin = RBAC-gated operations on the owning module"** (ARCHITECTURE §5.3) and the
project's **first cross-module event reaction** (one module publishes, another consumes idempotently). P5
adds the new `internal/modules/seller` module (canonical `domain / service / repo / handler / port` shape),
a fifth migration (`seller_applications`, `seller_stores`), a **fourth sqlc target** (`sellerdb`), and a
small identity seam (a role-write method the consumer calls).

**In scope**
- **Seller application** — `POST /api/v1/seller/applications` (apply; one active application per user, PRD
  FR-5) and `GET /api/v1/seller/application` (read own application). Schemas `SellerApplicationInput` /
  `SellerApplication`.
- **Admin lifecycle** — `POST /api/v1/admin/sellers/{sellerId}/approve` · `/reject` · `/suspend`, each behind
  `auth.RequireRole("admin")` (PRD FR-6). `{sellerId}` is the **application id**. The seller status machine
  is `pending → approved | rejected`, and `approved → suspended`; illegal transitions return **409**.
- **Store profile** — `GET`/`PATCH /api/v1/seller/store` for **approved** sellers (PRD FR-7). Schemas
  `Store` / `StoreInput`. The store row is created on approval; a `suspended` seller may read but not edit it.
- **Events published** — `SellerApproved` / `SellerSuspended`, written to the outbox **atomically** with the
  status change (the P2/P3 producer pattern).
- **First cross-module consumer** — `identity` **subscribes** to `SellerApproved` and promotes the user's
  role `buyer → seller` inside an idempotency transaction (`events.Idempotent`).
- **Seller read port** — `seller.Reader` (status lookup) exposed in `port.go` for **P6 catalog** to consume
  later; `seller` itself consumes the P4 **`identity.Reader`** to reject "already a seller" on apply.
- Migration `000005_seller`, a new `seller.sql`, and a new `sellerdb` sqlc target wired into `sqlc.yaml` +
  `SQLC_DIRS`.

**Out of scope (later phases)**
- The **admin list** endpoints `GET /admin/seller-applications` and `GET /admin/sellers` → **P15** (Admin
  Consolidation). P5 ships only the lifecycle *actions*; the demo takes the application id from the apply
  response.
- **Products / variants** gated on seller status (FR-7/FR-11) and the **consumption of `SellerSuspended`**
  to hide products → **P6** (catalog consumes the event and the `seller.Reader` port established here).
- **Seller reports** (FR-37–FR-39) → **P14**; **seller sub-order fulfilment** (Orders (Seller) tag) → **P13**.
- A **seller-revert-to-buyer** on suspension — suspension is a *status*, not a role change (see §6).
- **Rate-limiting** and the **import-boundary lint gate** → **P18** (conventions now, enforced later).

**Depends on.** P3 (roles, `auth` RBAC, the module template, the outbox producer pattern), P2 (events:
bus + outbox + `Idempotent`), P4 (the `identity.Reader` port + the ownership contract).

---

## 2. Prerequisites (Windows / PowerShell)

P5 adds **no new tools and no new Go dependencies** — everything (`pgx`, `sqlc`, `golang-migrate`,
`google/uuid`, the `events`/`auth`/`httpx` platform packages) is present from P0–P4.

| Need | Why | Command |
|---|---|---|
| Compose Postgres running | migrations + integration tests | `task db:up` |
| P4 migration applied | the schema must be at version `4` before adding `000005` | `task migrate:up` → `task migrate:version` ⇒ `4` |
| `AUTH_JWT_SECRET` set (≥32 bytes) | the server + `Authn`/`RequireRole` middleware | already in `.env` from P3 |
| An **admin** account | to exercise approve/reject/suspend (no admin-creation endpoint yet) | register a user, then `UPDATE identity_users SET role='admin'` (see §11) |

> No `go get` in this phase. If `task migrate:version` is below `4`, finish P4 first.

---

## 3. Tech stack & versions

Unchanged from P3/P4 — P5 reuses the established stack. The **new patterns** (not new tech) are listed for clarity.

| Concern | Choice (carried from P3/P4 unless noted) |
|---|---|
| New module shape | the P3 canonical template copied wholesale: `domain / service / repo / handler / port.go` |
| Transport | stdlib `net/http` `ServeMux`; path param via `r.PathValue("sellerId")` (Go 1.22 mux) |
| RBAC | `auth.Authn` + **`auth.RequireRole("admin")` / `RequireRole("seller")`** (P3) — first real role-gated routes |
| Cross-module **read** (sync) | `identity.Reader.UserByID` (P4 port) — reject "already a seller" on apply (Rule 4: sync for reads) |
| Cross-module **reaction** (async) | **new** — `seller` publishes; `identity` consumes via `bus.Subscribe` + `events.Idempotent(pool, "identity", …)` |
| Producer atomicity | the P3 pattern: state change + `outbox.Write(ctx, tx, evt)` in one `db.RunInTx` |
| Status state machine | `text` column + CHECK; guarded transitions in the service (illegal → 409) |
| One-active-application | Postgres **partial unique index** `WHERE status IN ('pending','approved','suspended')` + service pre-check |
| Queries / codegen | **sqlc** — a **fourth** `sql:` block emitting `internal/modules/seller/repo/sellerdb` |
| Unit-test mock | pure-Go fakes (`fakeRepo`, `fakeReader`) for the transition guards; DB-backed for the rest |

---

## 4. Target file tree (delta on P4)

```text
eco-api/
├── sqlc.yaml                                          # CHANGED: + fourth sql block (sellerdb)
├── Taskfile.yml                                       # CHANGED: SQLC_DIRS += seller gen dir
├── cmd/api/main.go                                    # CHANGED: build seller module + subscribe identity to SellerApproved
├── migrations/
│   ├── 000005_seller.up.sql                           # NEW: seller_applications + seller_stores + one-active index
│   └── 000005_seller.down.sql                         # NEW
└── internal/modules/
    ├── identity/                                      # CHANGED (small): the role-write seam the consumer calls
    │   ├── service/
    │   │   ├── ports.go                               # CHANGED: + UpdateUserRole repo method
    │   │   ├── promotion.go                           # NEW: PromoteToSeller(ctx, tx, userID)
    │   │   └── service_test.go                        # CHANGED: fakeRepo gains a no-op UpdateUserRole
    │   └── repo/
    │       ├── repo.go                                # CHANGED: + UpdateUserRole
    │       ├── queries/identity.sql                   # CHANGED: + UpdateUserRole query
    │       └── identitydb/                            # REGENERATED + committed (task sqlc)
    └── seller/                                        # NEW MODULE (copies the P3 template)
        ├── port.go                                    # PUBLIC surface: events + payloads + Reader (status) port
        ├── domain/
        │   ├── seller.go                              # Application, Store, Status + transition guards
        │   ├── events.go                              # SellerApproved/SellerSuspended consts + payloads
        │   └── errors.go                              # sentinels (ErrApplicationExists, ErrNotApprovable, …)
        ├── service/
        │   ├── ports.go                               # Repository + Outbox ports (service declares what it needs)
        │   ├── service.go                             # Apply/GetMyApplication/Approve/Reject/Suspend/Store + SellerStatus
        │   └── service_test.go                        # transition guards with fakes (no DB)
        ├── repo/
        │   ├── repo.go                                # adapter: sellerdb → domain, satisfies service.Repository
        │   ├── queries/seller.sql                     # sqlc queries
        │   ├── sellerdb/                              # GENERATED + committed
        │   └── seller_integration_test.go             # //go:build integration: apply→approve→store; flip; guards
        └── handler/
            ├── handler.go                             # DTOs + decode→service→encode (httpx envelope)
            └── routes.go                              # Mount: /seller/* and /admin/sellers/* with RBAC
```

**Import-direction rule (carried from P3/P4):** `seller/domain` and `seller/service` stay pure — stdlib,
`google/uuid`, the module's own `domain`, ports (`platform/db`, `platform/events`), and **sibling port
packages** (`internal/modules/identity` — only `port.go`, never its `service`/`repo`/`domain`). `service`
may name `pgx.Tx`/`db.Beginner` as the unit-of-work currency (the P1 §6 allow-listed exception). Only
`repo/` imports the `pgx` driver. **The identity package gains no `seller` import** — the consumer closure
lives in `cmd/api/main.go` (the composition root, which alone knows both modules).

---

## 5. Execution steps

Work top to bottom; each step ends in a check. Assumes P4 is complete (schema at version `4`, `identity`
module, `auth`, `httpx`, `events`, `db.RunInTx`, compose Postgres).

### S1 — Migration: `seller_applications` + `seller_stores`
```powershell
task migrate:new -- seller     # creates migrations/000005_seller.{up,down}.sql
```
Fill the up/down files (full contents in §8): both tables (`seller_` prefix), a per-user index, the
**partial unique index** enforcing one active application, and the status `CHECK`. `user_id` / `seller_id`
are **plain UUIDs** — no FK to `identity_users` (cross-module). Then:

```powershell
task migrate:up
task migrate:version           # -> 5
```
**Check:** `migrate:version` prints `5` (not dirty); `seller_applications` + `seller_stores` exist with
`uq_seller_applications_one_active`.

### S2 — sqlc: fourth target (`sellerdb`)
Append the `sellerdb` block to [../../sqlc.yaml](../../sqlc.yaml) (reuse the timestamptz/uuid overrides),
create `internal/modules/seller/repo/queries/seller.sql` (full in §8), and add the new gen dir to
`SQLC_DIRS` in [../../Taskfile.yml](../../Taskfile.yml). Then:

```powershell
task sqlc                       # generates internal/modules/seller/repo/sellerdb/*
```
**Check:** `go build ./internal/modules/seller/repo/sellerdb`; `task sqlc:check` clean after commit.

### S3 — Identity seam: role write
Add `UpdateUserRole` to `identity` (query → repo → `Repository` port) and `service/promotion.go`
(`PromoteToSeller`), then regenerate `identitydb` (full in §8). Extend the identity `fakeRepo` with a no-op.
```powershell
task sqlc
```
**Check:** `go build ./internal/modules/identity/...`; `task test` still green for identity.

### S4 — Seller domain
Create `internal/modules/seller/domain/{seller.go, events.go, errors.go}` (full in §8): the `Application`
and `Store` value objects, the `Status` type + transition guards, the event consts + payloads, and the
sentinel errors. Pure Go.
**Check:** `go build ./internal/modules/seller/domain`.

### S5 — Seller service + ports + port.go
Create `service/ports.go` (the `Repository` + `Outbox` ports the service declares), `service/service.go`
(the use cases, consuming `identity.Reader`), and the module's `port.go` (events + payloads + `Reader`).
Full in §8.
**Check:** `go build ./internal/modules/seller/service ./internal/modules/seller`.

### S6 — Seller repo adapter
Create `repo/repo.go` mapping `sellerdb` rows ↔ domain (full in §8); writes take `pgx.Tx`, reads take `ctx`
and return `pgx.ErrNoRows` (the P3 convention).
**Check:** `go build ./internal/modules/seller/repo`.

### S7 — Seller handler + routes
Create `handler/handler.go` (DTOs, validation, the seven handlers) and `handler/routes.go` (mount under
`/api/v1` with `Authn`, `RequireRole("seller")` for the store, `RequireRole("admin")` for the lifecycle).
Full in §8.
**Check:** `go build ./internal/modules/seller/...`.

### S8 — Wire in `main`, then run + test
Edit `cmd/api/main.go` (full file in §8): build `seller` repo → service → handler (passing `identitySvc` as
`identity.Reader`), **subscribe identity to `SellerApproved`** before the dispatcher starts, and mount the
routes.
```powershell
task run                        # boots; apply → approve → (after dispatch) re-login shows role "seller"
task test                       # unit: transition guards, already-seller, identity still green
task test:integration           # apply/approve/suspend + atomic outbox + cross-module flip + dedupe
task ci                         # tidy → sqlc generate → lint → test → build -> green
```
**Check:** `task ci` green; both suites pass.

---

## 6. The event-driven cross-module reaction & admin-on-owning-module contract

This is the discipline P5 establishes — the analog of P3's auth/module template, P4's ownership rule. It
realizes ARCHITECTURE **Rule 3** (no cross-module FK/joins; references by **id**, resolved via ports or
events), **Rule 4** (*event-driven first*; synchronous calls reserved for **reads** a request cannot proceed
without), **§5.3** (*admin is not a module*), and **§10** (RBAC).

**Admin = RBAC-gated operations on the owning module.**
- There is **no admin module**. `approve` / `reject` / `suspend` are handlers on the **seller** module (the
  owner of seller state), each wrapped with `auth.Authn` + `auth.RequireRole("admin")`. P6 will do the same
  for product moderation on `catalog`, P14 for metrics on `report`.

**Status is the source of truth — role is a snapshot.**
- The seller **status** (`pending`/`approved`/`rejected`/`suspended`) lives in `seller` and is the authority
  other modules read (via `seller.Reader`, consumed by P6 to hide a suspended seller's products).
- The **role** (`identity_users.role`) is flipped to `seller` on approval *only so the JWT can carry it* for
  `RequireRole("seller")`. **Suspension does not change the role** — a suspended user keeps `role=seller`
  but `status=suspended`, so seller **writes** must check status, not just role: store `PATCH` while
  suspended → **403**. (`RequireRole("seller")` alone is insufficient.)

**The role transition is a cross-module event reaction (not a sync write).**
- `seller.Approve` writes `status=approved`, creates the store, and `outbox.Write`s `SellerApproved` — all
  in one `db.RunInTx` (atomic producer pattern).
- `identity` **subscribes**: `bus.Subscribe(seller.EventSellerApproved, events.Idempotent(pool, "identity",
  h))`, where `h` decodes the payload and calls `identitySvc.PromoteToSeller(ctx, tx, userID)` **inside the
  dedupe tx**. The producing transaction never reaches across the module boundary; delivery is at-least-once
  and the consumer dedupes by `(consumer, event_id)`.
- **Eventual consistency is correct here:** the new role only matters at the next login/refresh, which
  necessarily follows approval (the JWT is minted from `identity_users.role`). The dispatcher delivers within
  `OUTBOX_POLL_INTERVAL`.

**The one sync cross-module call is a read.** On `apply`, `seller` calls `identity.Reader.UserByID(caller)`
and rejects with **409** if the role is already `seller` — a read a request cannot proceed without (Rule 4).

| Concern | Type / helper |
|---|---|
| Gate an admin action | `auth.Authn` + `auth.RequireRole("admin")` (httpx middleware) |
| Gate a seller write | `RequireRole("seller")` **and** a service check that status == `approved` (→ 403 otherwise) |
| Publish atomically | `outbox.Write(ctx, tx, evt)` inside `db.RunInTx` (P3 producer pattern) |
| React to a sibling's event | `bus.Subscribe(type, events.Idempotent(pool, consumer, txHandler))` (P2) |
| Resolve a user (sync read) | `identity.Reader.UserByID(ctx, id)` → `identity.PublicUser` |
| Expose seller status to siblings | `seller.Reader.SellerStatus(ctx, userID)` (P6 consumes) |

**Contracts & events (from IMPLEMENTATION_PLAN).** seller public port (status lookups for other modules);
`SellerApproved` / `SellerSuspended` (consumed by P6 for product hiding and P16 for notifications;
`SellerApproved` additionally consumed by **identity** here for the role flip).

---

## 7. Configuration reference (additions to P4)

**None.** P5 introduces no new environment variables. The existing `OUTBOX_POLL_INTERVAL` / `OUTBOX_BATCH_SIZE`
(P2) govern how quickly the `SellerApproved` consumer runs after approval; all `AUTH_*`, `DB_*`, `HTTP_*`,
`LOG_*` settings are unchanged and sufficient.

---

## 8. Full file contents

**`migrations/000005_seller.up.sql`**
```sql
-- P5 Seller Onboarding & Store: the seller module's tables (seller_ prefix; P1 ownership rule).
-- user_id / seller_id are plain UUIDs that reference the identity module BY ID ONLY — there is NO
-- cross-module foreign key (identity_users is owned by another module; ARCHITECTURE Rule 3).

CREATE TABLE seller_applications (
    id            uuid        PRIMARY KEY,
    user_id       uuid        NOT NULL,
    status        text        NOT NULL DEFAULT 'pending'
                              CHECK (status IN ('pending', 'approved', 'rejected', 'suspended')),
    store_name    text        NOT NULL,
    description   text        NOT NULL DEFAULT '',
    contact       text        NOT NULL,
    reject_reason text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    decided_at    timestamptz                            -- set when an admin approves/rejects/suspends
);

CREATE INDEX idx_seller_applications_user ON seller_applications (user_id);

-- One ACTIVE application per user (PRD FR-5). A rejected user may re-apply, so 'rejected' is excluded.
CREATE UNIQUE INDEX uq_seller_applications_one_active
    ON seller_applications (user_id)
    WHERE status IN ('pending', 'approved', 'suspended');

CREATE TABLE seller_stores (
    id          uuid        PRIMARY KEY,
    seller_id   uuid        NOT NULL UNIQUE,             -- = identity user id; one store per seller
    name        text        NOT NULL,
    logo_url    text        NOT NULL DEFAULT '',
    description text        NOT NULL DEFAULT '',
    contact     text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
```

**`migrations/000005_seller.down.sql`**
```sql
DROP TABLE IF EXISTS seller_stores;
DROP TABLE IF EXISTS seller_applications;
```

**`sqlc.yaml`** (append the fourth `sql:` block; keep the P1/P2/P3 blocks unchanged)
```yaml
  - engine: postgresql
    schema: "migrations/*.up.sql"
    queries: "internal/modules/seller/repo/queries"
    gen:
      go:
        package: "sellerdb"
        out: "internal/modules/seller/repo/sellerdb"
        sql_package: "pgx/v5"
        emit_interface: true
        emit_empty_slices: true
        overrides:
          - db_type: "timestamptz"
            go_type: "time.Time"
          - db_type: "uuid"
            go_type: "github.com/google/uuid.UUID"
```

**`Taskfile.yml`** — extend `SQLC_DIRS` (the `sqlc:check` guard) to include the new gen dir:
```yaml
  SQLC_DIRS: internal/platform/db/dbgen internal/platform/events/eventsdb internal/modules/identity/repo/identitydb internal/modules/seller/repo/sellerdb
```

**`internal/modules/seller/repo/queries/seller.sql`**
```sql
-- name: InsertApplication :exec
INSERT INTO seller_applications (id, user_id, status, store_name, description, contact, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetApplicationByID :one
SELECT id, user_id, status, store_name, description, contact, reject_reason, created_at
FROM seller_applications WHERE id = $1;

-- name: GetActiveApplicationByUser :one
SELECT id, user_id, status, store_name, description, contact, reject_reason, created_at
FROM seller_applications
WHERE user_id = $1 AND status IN ('pending', 'approved', 'suspended');

-- name: GetLatestApplicationByUser :one
SELECT id, user_id, status, store_name, description, contact, reject_reason, created_at
FROM seller_applications WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1;

-- name: UpdateApplicationStatus :exec
UPDATE seller_applications SET status = $2, reject_reason = $3, decided_at = now() WHERE id = $1;

-- name: InsertStore :exec
INSERT INTO seller_stores (id, seller_id, name, logo_url, description, contact)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetStoreBySeller :one
SELECT id, seller_id, name, logo_url, description, contact
FROM seller_stores WHERE seller_id = $1;

-- name: UpdateStore :exec
UPDATE seller_stores SET name = $2, logo_url = $3, description = $4, contact = $5, updated_at = now()
WHERE seller_id = $1;
```

> **sqlc output names** (from `task sqlc`): a model `SellerApplication` / `SellerStore`; param structs
> `InsertApplicationParams`, `UpdateApplicationStatusParams{ID, Status, RejectReason}`, `InsertStoreParams`,
> `UpdateStoreParams`. The four `:one` selects read a fixed column subset, so each returns its own row
> struct (`GetApplicationByIDRow`, `GetActiveApplicationByUserRow`, `GetLatestApplicationByUserRow`,
> `GetStoreBySellerRow`) — all with the listed fields; the repo maps them through positional helpers
> (`toApplication`, `toStore`) so the concrete row-type name does not matter.

### Identity seam (CHANGED)

**`internal/modules/identity/repo/queries/identity.sql`** — append:
```sql
-- name: UpdateUserRole :exec
UPDATE identity_users SET role = $2 WHERE id = $1;
```

**`internal/modules/identity/repo/repo.go`** — append this method (P3/P4 methods unchanged):
```go
func (r *Repo) UpdateUserRole(ctx context.Context, tx pgx.Tx, userID uuid.UUID, role string) error {
	return r.q.WithTx(tx).UpdateUserRole(ctx, identitydb.UpdateUserRoleParams{ID: userID, Role: role})
}
```

**`internal/modules/identity/service/ports.go`** — add one line to the `Repository` interface:
```go
	// --- seller promotion (P5): the SellerApproved consumer flips the role in its dedupe tx ---
	UpdateUserRole(ctx context.Context, tx pgx.Tx, userID uuid.UUID, role string) error
```

**`internal/modules/identity/service/promotion.go`** (NEW)
```go
package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
)

// PromoteToSeller sets a user's role to seller. It is called by the SellerApproved consumer (wired in
// cmd/api/main.go) inside the events.Idempotent dedupe transaction, so "role flipped" and "event marked
// processed" commit together. Idempotent: re-running on an already-seller user is a no-op UPDATE.
func (s *Service) PromoteToSeller(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	return s.repo.UpdateUserRole(ctx, tx, userID, string(domain.RoleSeller))
}
```

**`internal/modules/identity/service/service_test.go`** — append a no-op so `fakeRepo` still satisfies the
grown `Repository`:
```go
func (fakeRepo) UpdateUserRole(context.Context, pgx.Tx, uuid.UUID, string) error { return nil }
```

### New module: `seller`

**`internal/modules/seller/domain/seller.go`**
```go
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Status is the seller lifecycle state (PRD FR-5/FR-6/FR-8; mirrors OpenAPI SellerStatus).
type Status string

const (
	StatusPending   Status = "pending"
	StatusApproved  Status = "approved"
	StatusRejected  Status = "rejected"
	StatusSuspended Status = "suspended"
)

// RoleSeller is the identity role value a user holds once approved (compared against identity.PublicUser.Role
// without importing identity/domain).
const RoleSeller = "seller"

// Application is a buyer's request to become a seller. reject_reason/decided_at are server-owned and not
// part of the public DTO.
type Application struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	Status       Status
	StoreName    string
	Description  string
	Contact      string
	RejectReason string
	CreatedAt    time.Time
}

// Store is an approved seller's public storefront profile.
type Store struct {
	ID          uuid.UUID
	SellerID    uuid.UUID // = identity user id
	Name        string
	LogoURL     string
	Description string
	Contact     string
}

// Transition guards — an admin action is legal only from a specific source status (illegal → 409).
func (a Application) CanApprove() bool { return a.Status == StatusPending }
func (a Application) CanReject() bool  { return a.Status == StatusPending }
func (a Application) CanSuspend() bool { return a.Status == StatusApproved }
```

**`internal/modules/seller/domain/events.go`**
```go
package domain

import "github.com/google/uuid"

// Events published (atomically, via the outbox) on the seller lifecycle.
// SellerApproved is consumed by identity (role flip, P5) + catalog/notification (P6/P16);
// SellerSuspended is consumed by catalog (hide products, P6) + notification (P16).
const (
	EventSellerApproved  = "SellerApproved"
	EventSellerSuspended = "SellerSuspended"
)

type SellerApprovedPayload struct {
	UserID        uuid.UUID `json:"user_id"`
	ApplicationID uuid.UUID `json:"application_id"`
}

type SellerSuspendedPayload struct {
	UserID        uuid.UUID `json:"user_id"`
	ApplicationID uuid.UUID `json:"application_id"`
}
```

**`internal/modules/seller/domain/errors.go`**
```go
package domain

import "errors"

var (
	ErrApplicationExists   = errors.New("an active seller application already exists")
	ErrAlreadySeller       = errors.New("user is already a seller")
	ErrApplicationNotFound = errors.New("seller application not found")
	ErrStoreNotFound       = errors.New("store not found")
	ErrNotApprovable       = errors.New("application is not in a state that can be approved")
	ErrNotRejectable       = errors.New("application is not in a state that can be rejected")
	ErrNotSuspendable      = errors.New("seller is not in a state that can be suspended")
	ErrNotApproved         = errors.New("seller is not approved") // store edits require an approved seller
)
```

**`internal/modules/seller/port.go`**
```go
// Package seller is the public port surface of the seller module: the events it publishes and the
// Reader port other modules (P6 catalog) consume to look up a seller's status.
package seller

import (
	"context"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
)

// Published events (the single public surface for producers/consumers).
const (
	EventSellerApproved  = domain.EventSellerApproved
	EventSellerSuspended = domain.EventSellerSuspended
)

// Payload aliases so consumers (the identity subscriber in main.go, P6 catalog) decode without importing
// seller/domain.
type (
	SellerApprovedPayload  = domain.SellerApprovedPayload
	SellerSuspendedPayload = domain.SellerSuspendedPayload
	Status                 = domain.Status
)

// Reader is the read port sibling modules consume to gate on seller status (P6 hides a suspended seller's
// products). They import ONLY this file. *service.Service satisfies it.
type Reader interface {
	SellerStatus(ctx context.Context, userID uuid.UUID) (Status, error)
}
```

**`internal/modules/seller/service/ports.go`**
```go
package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

// Repository is the persistence port the service needs; repo/ implements it over sqlc. Write methods take
// pgx.Tx so the service composes them with the outbox in one RunInTx; read methods take ctx and return
// pgx.ErrNoRows when absent (the service maps that to domain errors).
type Repository interface {
	InsertApplication(ctx context.Context, tx pgx.Tx, a domain.Application) error
	GetApplicationByID(ctx context.Context, id uuid.UUID) (domain.Application, error)
	GetActiveApplicationByUser(ctx context.Context, userID uuid.UUID) (domain.Application, error)
	GetLatestApplicationByUser(ctx context.Context, userID uuid.UUID) (domain.Application, error)
	UpdateApplicationStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status, rejectReason string) error

	InsertStore(ctx context.Context, tx pgx.Tx, s domain.Store) error
	GetStoreBySeller(ctx context.Context, sellerID uuid.UUID) (domain.Store, error)
	UpdateStore(ctx context.Context, tx pgx.Tx, s domain.Store) error
}

// Outbox is the publish port (satisfied by *events.Outbox) — kept narrow for testability.
type Outbox interface {
	Write(ctx context.Context, tx pgx.Tx, e events.Event) error
}
```

**`internal/modules/seller/service/service.go`**
```go
package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "github.com/amoorihesham/eco-api/internal/modules/identity"
	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

// Service implements the seller use cases. It depends only on ports: its Repository, the Outbox, and the
// identity.Reader (P4) for the one synchronous cross-module read (the "already a seller" guard).
type Service struct {
	pool   db.Beginner
	repo   Repository
	users  identity.Reader
	outbox Outbox
}

func New(pool db.Beginner, repo Repository, users identity.Reader, outbox Outbox) *Service {
	return &Service{pool: pool, repo: repo, users: users, outbox: outbox}
}

// ApplicationInput / StoreInput carry the editable fields (ids/status/timestamps are server-owned).
type ApplicationInput struct {
	StoreName   string
	Description string
	Contact     string
}

type StoreInput struct {
	Name        string
	LogoURL     string
	Description string
	Contact     string
}

// Apply submits a seller application. Rejects if the caller is already a seller (sync read via
// identity.Reader) or already has an active application (PRD FR-5).
func (s *Service) Apply(ctx context.Context, userID uuid.UUID, in ApplicationInput) (domain.Application, error) {
	u, err := s.users.UserByID(ctx, userID)
	if err != nil {
		return domain.Application{}, err
	}
	if u.Role == domain.RoleSeller {
		return domain.Application{}, domain.ErrAlreadySeller
	}
	if _, err := s.repo.GetActiveApplicationByUser(ctx, userID); err == nil {
		return domain.Application{}, domain.ErrApplicationExists
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Application{}, err
	}

	a := domain.Application{
		ID:        uuid.New(),
		UserID:    userID,
		Status:    domain.StatusPending,
		StoreName: in.StoreName,
		Description: in.Description,
		Contact:   in.Contact,
		CreatedAt: time.Now().UTC(),
	}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.InsertApplication(ctx, tx, a)
	}); err != nil {
		return domain.Application{}, err
	}
	return a, nil
}

// GetMyApplication returns the caller's latest application (any status); none → ErrApplicationNotFound.
func (s *Service) GetMyApplication(ctx context.Context, userID uuid.UUID) (domain.Application, error) {
	a, err := s.repo.GetLatestApplicationByUser(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Application{}, domain.ErrApplicationNotFound
		}
		return domain.Application{}, err
	}
	return a, nil
}

// Approve (admin) moves a pending application to approved, creates the store, and publishes SellerApproved
// — all atomically. The role flip happens asynchronously in the identity consumer.
func (s *Service) Approve(ctx context.Context, appID uuid.UUID) (domain.Application, error) {
	a, err := s.getForDecision(ctx, appID)
	if err != nil {
		return domain.Application{}, err
	}
	if !a.CanApprove() {
		return domain.Application{}, domain.ErrNotApprovable
	}
	store := domain.Store{ID: uuid.New(), SellerID: a.UserID, Name: a.StoreName, Contact: a.Contact}
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.UpdateApplicationStatus(ctx, tx, a.ID, string(domain.StatusApproved), ""); err != nil {
			return err
		}
		if err := s.repo.InsertStore(ctx, tx, store); err != nil {
			return err
		}
		evt, err := events.NewEvent(domain.EventSellerApproved,
			domain.SellerApprovedPayload{UserID: a.UserID, ApplicationID: a.ID})
		if err != nil {
			return err
		}
		return s.outbox.Write(ctx, tx, evt) // atomic publish (P2 §6 / P3 producer pattern)
	})
	if err != nil {
		return domain.Application{}, err
	}
	a.Status = domain.StatusApproved
	return a, nil
}

// Reject (admin) moves a pending application to rejected with an optional reason. No event.
func (s *Service) Reject(ctx context.Context, appID uuid.UUID, reason string) (domain.Application, error) {
	a, err := s.getForDecision(ctx, appID)
	if err != nil {
		return domain.Application{}, err
	}
	if !a.CanReject() {
		return domain.Application{}, domain.ErrNotRejectable
	}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.UpdateApplicationStatus(ctx, tx, a.ID, string(domain.StatusRejected), reason)
	}); err != nil {
		return domain.Application{}, err
	}
	a.Status = domain.StatusRejected
	a.RejectReason = reason
	return a, nil
}

// Suspend (admin) moves an approved seller to suspended and publishes SellerSuspended (P6 hides products).
// The role stays seller — status is the source of truth.
func (s *Service) Suspend(ctx context.Context, appID uuid.UUID) (domain.Application, error) {
	a, err := s.getForDecision(ctx, appID)
	if err != nil {
		return domain.Application{}, err
	}
	if !a.CanSuspend() {
		return domain.Application{}, domain.ErrNotSuspendable
	}
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.UpdateApplicationStatus(ctx, tx, a.ID, string(domain.StatusSuspended), ""); err != nil {
			return err
		}
		evt, err := events.NewEvent(domain.EventSellerSuspended,
			domain.SellerSuspendedPayload{UserID: a.UserID, ApplicationID: a.ID})
		if err != nil {
			return err
		}
		return s.outbox.Write(ctx, tx, evt)
	})
	if err != nil {
		return domain.Application{}, err
	}
	a.Status = domain.StatusSuspended
	return a, nil
}

// GetStore returns the caller's store; none → ErrStoreNotFound.
func (s *Service) GetStore(ctx context.Context, userID uuid.UUID) (domain.Store, error) {
	st, err := s.repo.GetStoreBySeller(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Store{}, domain.ErrStoreNotFound
		}
		return domain.Store{}, err
	}
	return st, nil
}

// UpdateStore edits the caller's store. The caller must be an APPROVED seller (a suspended seller keeps
// role=seller but cannot edit → ErrNotApproved → 403).
func (s *Service) UpdateStore(ctx context.Context, userID uuid.UUID, in StoreInput) (domain.Store, error) {
	status, err := s.SellerStatus(ctx, userID)
	if err != nil {
		return domain.Store{}, err
	}
	if status != domain.StatusApproved {
		return domain.Store{}, domain.ErrNotApproved
	}
	st, err := s.repo.GetStoreBySeller(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Store{}, domain.ErrStoreNotFound
		}
		return domain.Store{}, err
	}
	updated := domain.Store{
		ID:          st.ID,
		SellerID:    userID,
		Name:        in.Name,
		LogoURL:     in.LogoURL,
		Description: in.Description,
		Contact:     in.Contact,
	}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.UpdateStore(ctx, tx, updated)
	}); err != nil {
		return domain.Store{}, err
	}
	return updated, nil
}

// SellerStatus satisfies seller.Reader — sibling modules (P6) gate on it. No active application → not a
// seller → ErrApplicationNotFound.
func (s *Service) SellerStatus(ctx context.Context, userID uuid.UUID) (domain.Status, error) {
	a, err := s.repo.GetActiveApplicationByUser(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrApplicationNotFound
		}
		return "", err
	}
	return a.Status, nil
}

// getForDecision loads an application by id for an admin action; missing → ErrApplicationNotFound.
func (s *Service) getForDecision(ctx context.Context, appID uuid.UUID) (domain.Application, error) {
	a, err := s.repo.GetApplicationByID(ctx, appID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Application{}, domain.ErrApplicationNotFound
		}
		return domain.Application{}, err
	}
	return a, nil
}
```

**`internal/modules/seller/repo/repo.go`**
```go
package repo

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
	"github.com/amoorihesham/eco-api/internal/modules/seller/repo/sellerdb"
)

// Repo implements service.Repository over sqlc-generated queries.
type Repo struct{ q *sellerdb.Queries }

func New(pool *pgxpool.Pool) *Repo { return &Repo{q: sellerdb.New(pool)} }

func (r *Repo) InsertApplication(ctx context.Context, tx pgx.Tx, a domain.Application) error {
	return r.q.WithTx(tx).InsertApplication(ctx, sellerdb.InsertApplicationParams{
		ID:        a.ID,
		UserID:    a.UserID,
		Status:    string(a.Status),
		StoreName: a.StoreName,
		Description: a.Description,
		Contact:   a.Contact,
		CreatedAt: a.CreatedAt,
	})
}

func (r *Repo) GetApplicationByID(ctx context.Context, id uuid.UUID) (domain.Application, error) {
	row, err := r.q.GetApplicationByID(ctx, id)
	if err != nil {
		return domain.Application{}, err
	}
	return toApplication(row.ID, row.UserID, row.Status, row.StoreName, row.Description, row.Contact, row.RejectReason, row.CreatedAt), nil
}

func (r *Repo) GetActiveApplicationByUser(ctx context.Context, userID uuid.UUID) (domain.Application, error) {
	row, err := r.q.GetActiveApplicationByUser(ctx, userID)
	if err != nil {
		return domain.Application{}, err
	}
	return toApplication(row.ID, row.UserID, row.Status, row.StoreName, row.Description, row.Contact, row.RejectReason, row.CreatedAt), nil
}

func (r *Repo) GetLatestApplicationByUser(ctx context.Context, userID uuid.UUID) (domain.Application, error) {
	row, err := r.q.GetLatestApplicationByUser(ctx, userID)
	if err != nil {
		return domain.Application{}, err
	}
	return toApplication(row.ID, row.UserID, row.Status, row.StoreName, row.Description, row.Contact, row.RejectReason, row.CreatedAt), nil
}

func (r *Repo) UpdateApplicationStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status, rejectReason string) error {
	return r.q.WithTx(tx).UpdateApplicationStatus(ctx, sellerdb.UpdateApplicationStatusParams{
		ID: id, Status: status, RejectReason: rejectReason,
	})
}

func (r *Repo) InsertStore(ctx context.Context, tx pgx.Tx, s domain.Store) error {
	return r.q.WithTx(tx).InsertStore(ctx, sellerdb.InsertStoreParams{
		ID:          s.ID,
		SellerID:    s.SellerID,
		Name:        s.Name,
		LogoUrl:     s.LogoURL,
		Description: s.Description,
		Contact:     s.Contact,
	})
}

func (r *Repo) GetStoreBySeller(ctx context.Context, sellerID uuid.UUID) (domain.Store, error) {
	row, err := r.q.GetStoreBySeller(ctx, sellerID)
	if err != nil {
		return domain.Store{}, err
	}
	return domain.Store{
		ID:          row.ID,
		SellerID:    row.SellerID,
		Name:        row.Name,
		LogoURL:     row.LogoUrl,
		Description: row.Description,
		Contact:     row.Contact,
	}, nil
}

func (r *Repo) UpdateStore(ctx context.Context, tx pgx.Tx, s domain.Store) error {
	return r.q.WithTx(tx).UpdateStore(ctx, sellerdb.UpdateStoreParams{
		SellerID:    s.SellerID,
		Name:        s.Name,
		LogoUrl:     s.LogoURL,
		Description: s.Description,
		Contact:     s.Contact,
	})
}

func toApplication(id, userID uuid.UUID, status, storeName, description, contact, rejectReason string, createdAt time.Time) domain.Application {
	return domain.Application{
		ID:           id,
		UserID:       userID,
		Status:       domain.Status(status),
		StoreName:    storeName,
		Description:  description,
		Contact:      contact,
		RejectReason: rejectReason,
		CreatedAt:    createdAt,
	}
}
```

**`internal/modules/seller/handler/handler.go`**
```go
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
	"github.com/amoorihesham/eco-api/internal/modules/seller/service"
	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

type Handler struct{ svc *service.Service }

func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

// --- DTOs (mirror the OpenAPI Seller schemas) ---

type applicationInput struct {
	StoreName   string `json:"store_name"`
	Description string `json:"description"`
	Contact     string `json:"contact"`
}

type rejectInput struct {
	Reason string `json:"reason"`
}

type storeInput struct {
	Name        string `json:"name"`
	LogoURL     string `json:"logo_url"`
	Description string `json:"description"`
	Contact     string `json:"contact"`
}

type applicationDTO struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Status      string `json:"status"`
	StoreName   string `json:"store_name"`
	Description string `json:"description,omitempty"`
	Contact     string `json:"contact"`
	CreatedAt   string `json:"created_at"`
}

type storeDTO struct {
	ID          string `json:"id"`
	SellerID    string `json:"seller_id"`
	Name        string `json:"name"`
	LogoURL     string `json:"logo_url,omitempty"`
	Description string `json:"description,omitempty"`
	Contact     string `json:"contact"`
}

// --- seller self-service ---

func (h *Handler) apply(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	var req applicationInput
	if !decode(w, r, &req) {
		return
	}
	var errs []httpx.ErrorDetail
	if strings.TrimSpace(req.StoreName) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "store_name", Message: "store_name is required"})
	}
	if strings.TrimSpace(req.Contact) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "contact", Message: "contact is required"})
	}
	if len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	a, err := h.svc.Apply(r.Context(), userID, service.ApplicationInput{
		StoreName:   strings.TrimSpace(req.StoreName),
		Description: strings.TrimSpace(req.Description),
		Contact:     strings.TrimSpace(req.Contact),
	})
	if err != nil {
		writeSellerError(w, err, "could not submit application")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toApplicationDTO(a))
}

func (h *Handler) getMyApplication(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	a, err := h.svc.GetMyApplication(r.Context(), userID)
	if err != nil {
		writeSellerError(w, err, "could not load application")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toApplicationDTO(a))
}

func (h *Handler) getMyStore(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	st, err := h.svc.GetStore(r.Context(), userID)
	if err != nil {
		writeSellerError(w, err, "could not load store")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toStoreDTO(st))
}

func (h *Handler) updateMyStore(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	var req storeInput
	if !decode(w, r, &req) {
		return
	}
	var errs []httpx.ErrorDetail
	if strings.TrimSpace(req.Name) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "name", Message: "name is required"})
	}
	if strings.TrimSpace(req.Contact) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "contact", Message: "contact is required"})
	}
	if len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	st, err := h.svc.UpdateStore(r.Context(), userID, service.StoreInput{
		Name:        strings.TrimSpace(req.Name),
		LogoURL:     strings.TrimSpace(req.LogoURL),
		Description: strings.TrimSpace(req.Description),
		Contact:     strings.TrimSpace(req.Contact),
	})
	if err != nil {
		writeSellerError(w, err, "could not update store")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toStoreDTO(st))
}

// --- admin lifecycle ---

func (h *Handler) approve(w http.ResponseWriter, r *http.Request) {
	id, ok := pathSellerID(w, r)
	if !ok {
		return
	}
	a, err := h.svc.Approve(r.Context(), id)
	if err != nil {
		writeSellerError(w, err, "could not approve seller")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toApplicationDTO(a))
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathSellerID(w, r)
	if !ok {
		return
	}
	var req rejectInput
	// Body is optional; ignore a decode error on an empty body.
	_ = json.NewDecoder(r.Body).Decode(&req)
	a, err := h.svc.Reject(r.Context(), id, strings.TrimSpace(req.Reason))
	if err != nil {
		writeSellerError(w, err, "could not reject seller")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toApplicationDTO(a))
}

func (h *Handler) suspend(w http.ResponseWriter, r *http.Request) {
	id, ok := pathSellerID(w, r)
	if !ok {
		return
	}
	a, err := h.svc.Suspend(r.Context(), id)
	if err != nil {
		writeSellerError(w, err, "could not suspend seller")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toApplicationDTO(a))
}

// --- helpers ---

func callerID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := auth.UserID(r.Context())
	if !ok {
		httpx.Unauthorized(w, "authentication required")
		return uuid.Nil, false
	}
	return id, true
}

func pathSellerID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("sellerId"))
	if err != nil {
		httpx.NotFound(w, "seller application not found")
		return uuid.Nil, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "invalid JSON body")
		return false
	}
	return true
}

// writeSellerError maps domain sentinels to the standard envelope.
func writeSellerError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, domain.ErrApplicationNotFound), errors.Is(err, domain.ErrStoreNotFound):
		httpx.NotFound(w, "not found")
	case errors.Is(err, domain.ErrAlreadySeller), errors.Is(err, domain.ErrApplicationExists):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict, err.Error())
	case errors.Is(err, domain.ErrNotApprovable), errors.Is(err, domain.ErrNotRejectable), errors.Is(err, domain.ErrNotSuspendable):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict, err.Error())
	case errors.Is(err, domain.ErrNotApproved):
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "seller is not approved")
	default:
		httpx.Internal(w, fallback)
	}
}

func toApplicationDTO(a domain.Application) applicationDTO {
	return applicationDTO{
		ID:          a.ID.String(),
		UserID:      a.UserID.String(),
		Status:      string(a.Status),
		StoreName:   a.StoreName,
		Description: a.Description,
		Contact:     a.Contact,
		CreatedAt:   a.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func toStoreDTO(s domain.Store) storeDTO {
	return storeDTO{
		ID:          s.ID.String(),
		SellerID:    s.SellerID.String(),
		Name:        s.Name,
		LogoURL:     s.LogoURL,
		Description: s.Description,
		Contact:     s.Contact,
	}
}
```

**`internal/modules/seller/handler/routes.go`**
```go
package handler

import (
	"net/http"

	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// Mount registers the seller routes under /api/v1. apply/getMyApplication need only authentication (a buyer
// applies); the store requires the seller role; the admin lifecycle requires the admin role. The caller id
// always comes from the verified token (auth.UserID), never the body/path.
func (h *Handler) Mount(mux *http.ServeMux, authn httpx.Middleware) {
	seller := func(next http.Handler) http.Handler { return authn(auth.RequireRole("seller")(next)) }
	admin := func(next http.Handler) http.Handler { return authn(auth.RequireRole("admin")(next)) }

	// Seller self-service
	mux.Handle("POST /api/v1/seller/applications", authn(http.HandlerFunc(h.apply)))
	mux.Handle("GET /api/v1/seller/application", authn(http.HandlerFunc(h.getMyApplication)))
	mux.Handle("GET /api/v1/seller/store", seller(http.HandlerFunc(h.getMyStore)))
	mux.Handle("PATCH /api/v1/seller/store", seller(http.HandlerFunc(h.updateMyStore)))

	// Admin lifecycle (RBAC-gated operations on the owning module — P5 §6)
	mux.Handle("POST /api/v1/admin/sellers/{sellerId}/approve", admin(http.HandlerFunc(h.approve)))
	mux.Handle("POST /api/v1/admin/sellers/{sellerId}/reject", admin(http.HandlerFunc(h.reject)))
	mux.Handle("POST /api/v1/admin/sellers/{sellerId}/suspend", admin(http.HandlerFunc(h.suspend)))
}
```

**`cmd/api/main.go`** (updated — adds the seller module + the SellerApproved consumer to the P4 version)
```go
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

	bus := events.NewBus(logger)
	dispatcher := events.NewDispatcher(pool, bus, logger, cfg.OutboxPollInterval, cfg.OutboxBatchSize)

	// --- identity module (P3/P4): auth adapters → repo → service → handler ---
	hasher := auth.NewBcryptHasher(cfg.AuthBcryptCost)
	jwt := auth.NewJWT(cfg.AuthJWTSecret, cfg.AuthAccessTTL)
	outbox := events.NewOutbox(pool)
	identitySvc := identityservice.New(pool, identityrepo.New(pool), hasher, jwt, outbox,
		identityservice.Config{RefreshTTL: cfg.AuthRefreshTTL, ResetTTL: cfg.AuthResetTTL})
	identityH := identityhandler.New(identitySvc)

	// --- seller module (P5): repo → service → handler; consumes identity.Reader for the apply guard ---
	sellerSvc := sellerservice.New(pool, sellerrepo.New(pool), identitySvc, outbox)
	sellerH := sellerhandler.New(sellerSvc)

	// First cross-module consumer (P5): identity reacts to SellerApproved by promoting the user's role.
	// Idempotent + at-least-once; the role flip and the processed-events mark commit in one tx.
	bus.Subscribe(seller.EventSellerApproved, events.Idempotent(pool, "identity",
		func(ctx context.Context, tx pgx.Tx, e events.Event) error {
			var p seller.SellerApprovedPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return err
			}
			return identitySvc.PromoteToSeller(ctx, tx, p.UserID)
		}))

	router := newRouter(logger, healthH, identityH, sellerH, jwt)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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

	<-dispatcherDone
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
```

> The P3 demo `GET /api/v1/admin/ping` route is now redundant (P5 ships real admin routes) — leave it or
> delete it; it is not part of P5's surface.

---

## 9. Testing plan

| Test | File | Asserts | Needs DB? |
|---|---|---|---|
| Transition guards | `seller/service/service_test.go` | approve/reject of a non-`pending` app → `ErrNotApprovable`/`ErrNotRejectable`; suspend of a non-`approved` → `ErrNotSuspendable` | no |
| Already-a-seller | `seller/service/service_test.go` | `Apply` when `identity.Reader` reports role `seller` → `ErrAlreadySeller` | no |
| identity still compiles | `identity/service/service_test.go` | the P3/P4 tests pass with `fakeRepo` gaining `UpdateUserRole` | no |
| Apply + one-active | `seller/repo/seller_integration_test.go` | apply → `pending`; second apply → `ErrApplicationExists` | yes |
| Approve atomic + store | `seller/repo/seller_integration_test.go` | approve → `approved` + a `seller_stores` row + **exactly one `SellerApproved`** outbox row | yes |
| Cross-module flip + dedupe | `seller/repo/seller_integration_test.go` | running the `SellerApproved` consumer flips `identity_users.role` to `seller`; replaying the same event is a no-op | yes |
| Suspend gates store | `seller/repo/seller_integration_test.go` | suspend → `suspended` + one `SellerSuspended` row; `UpdateStore` then → `ErrNotApproved` | yes |

**`internal/modules/seller/service/service_test.go`** (no Docker — fakes; guard paths only)
```go
package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "github.com/amoorihesham/eco-api/internal/modules/identity"
	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
	"github.com/amoorihesham/eco-api/internal/modules/seller/service"
)

// fakeRepo returns a canned application; only the read methods used before a tx are exercised here.
type fakeRepo struct {
	app domain.Application
	err error
}

func (f fakeRepo) GetApplicationByID(context.Context, uuid.UUID) (domain.Application, error) {
	return f.app, f.err
}
func (f fakeRepo) GetActiveApplicationByUser(context.Context, uuid.UUID) (domain.Application, error) {
	return f.app, f.err
}
func (f fakeRepo) GetLatestApplicationByUser(context.Context, uuid.UUID) (domain.Application, error) {
	return f.app, f.err
}
func (fakeRepo) InsertApplication(context.Context, pgx.Tx, domain.Application) error { return nil }
func (fakeRepo) UpdateApplicationStatus(context.Context, pgx.Tx, uuid.UUID, string, string) error {
	return nil
}
func (fakeRepo) InsertStore(context.Context, pgx.Tx, domain.Store) error          { return nil }
func (fakeRepo) GetStoreBySeller(context.Context, uuid.UUID) (domain.Store, error) { return domain.Store{}, pgx.ErrNoRows }
func (fakeRepo) UpdateStore(context.Context, pgx.Tx, domain.Store) error          { return nil }

// fakeReader stands in for identity.Reader.
type fakeReader struct {
	role string
	err  error
}

func (f fakeReader) UserByID(context.Context, uuid.UUID) (identity.PublicUser, error) {
	return identity.PublicUser{Role: f.role}, f.err
}

func TestApproveRejectsNonPending(t *testing.T) {
	repo := fakeRepo{app: domain.Application{Status: domain.StatusApproved}}
	// pool/outbox are nil: Approve returns on the guard before any transaction.
	svc := service.New(nil, repo, fakeReader{role: "buyer"}, nil)
	if _, err := svc.Approve(context.Background(), uuid.New()); !errors.Is(err, domain.ErrNotApprovable) {
		t.Fatalf("want ErrNotApprovable, got %v", err)
	}
}

func TestSuspendRequiresApproved(t *testing.T) {
	repo := fakeRepo{app: domain.Application{Status: domain.StatusPending}}
	svc := service.New(nil, repo, fakeReader{role: "buyer"}, nil)
	if _, err := svc.Suspend(context.Background(), uuid.New()); !errors.Is(err, domain.ErrNotSuspendable) {
		t.Fatalf("want ErrNotSuspendable, got %v", err)
	}
}

func TestApplyRejectsExistingSeller(t *testing.T) {
	svc := service.New(nil, fakeRepo{}, fakeReader{role: "seller"}, nil)
	if _, err := svc.Apply(context.Background(), uuid.New(), service.ApplicationInput{StoreName: "S", Contact: "c"}); !errors.Is(err, domain.ErrAlreadySeller) {
		t.Fatalf("want ErrAlreadySeller, got %v", err)
	}
}
```

**`internal/modules/seller/repo/seller_integration_test.go`** (build-tagged; against compose Postgres)
```go
//go:build integration

package repo_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
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
```

> The integration test names `platform_processed_events` / `platform_outbox` columns from P2; adjust the
> `TRUNCATE` / `SELECT` to the actual P2 table + column names if they differ.

Run: `task test` (unit) and `task test:integration` (DB-backed).

---

## 10. Definition of Done

- [ ] `task migrate:up` applies cleanly; `task migrate:version` → `5` (not dirty); `seller_applications` +
      `seller_stores` exist with the `uq_seller_applications_one_active` partial unique index.
- [ ] `task sqlc` emits `internal/modules/seller/repo/sellerdb/*` (and regenerates `identitydb` for
      `UpdateUserRole`); `task sqlc:check` reports no diff across all four gen dirs.
- [ ] `task run` boots; a buyer `POST /api/v1/seller/applications` → `201` (`status: "pending"`); a second
      apply → `409`; `GET /api/v1/seller/application` → `200`.
- [ ] An **admin** `POST /api/v1/admin/sellers/{id}/approve` → `200` (`status: "approved"`); approving again
      → `409`; a `reject` of a non-pending app → `409`; a non-admin caller → `403`.
- [ ] **Cross-module flip:** after approval + dispatch, the buyer re-logs-in and the token/user shows
      `role: "seller"`; the `SellerApproved` consumer runs **exactly once** per event (replay is a no-op).
- [ ] Store: an approved seller `GET`/`PATCH /api/v1/seller/store` → `200`; after `suspend`, `PATCH` →
      `403` while `GET` still works; `SellerSuspended` is published once.
- [ ] `task test` (unit) green — transition guards, already-seller, identity unchanged.
- [ ] `task test:integration` green — apply/one-active, approve atomicity, role flip + dedupe, suspend gating.
- [ ] `task ci` green (tidy → sqlc generate → lint → test → build).
- [ ] `seller/domain` + `seller/service` import no driver/SDK internals; the **identity package gains no
      `seller` import**; new tables hold the `seller_` prefix; **no cross-module FK**; no new env vars.

*Demo: a buyer applies; an admin approves; the buyer re-logs-in as a seller and edits their store; the admin
suspends them and the next store edit is rejected with 403.*

---

## 11. Verification (PowerShell)

```powershell
# 1. Migrate + generate
task db:up
task migrate:up
task migrate:version          # -> 5
task sqlc

# 2. Build pipeline
task ci                       # tidy, sqlc generate, lint, test, build -> green

# 3. Run, then register a buyer and an admin (second terminal, after `task run`)
$buyer = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/register -ContentType application/json `
  -Body (@{ email = "seller@example.com"; password = "password123"; name = "Seller One" } | ConvertTo-Json)
$bh = @{ Authorization = "Bearer $($buyer.tokens.access_token)" }

Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/register -ContentType application/json `
  -Body (@{ email = "admin@example.com"; password = "password123"; name = "Admin" } | ConvertTo-Json) | Out-Null
# No admin-creation endpoint yet (P5): promote directly, then log in to get an admin JWT.
docker compose exec postgres psql -U eco -d eco -c "UPDATE identity_users SET role='admin' WHERE email='admin@example.com';"
$admin = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/login -ContentType application/json `
  -Body (@{ email = "admin@example.com"; password = "password123" } | ConvertTo-Json)
$ah = @{ Authorization = "Bearer $($admin.tokens.access_token)" }

# 4. Buyer applies -> pending
$app = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/seller/applications -Headers $bh -ContentType application/json `
  -Body (@{ store_name = "Acme"; contact = "acme@x.com" } | ConvertTo-Json)
$app.status                   # -> pending

# 5. Admin approves (the application id is the {sellerId})
Invoke-RestMethod -Method Post -Uri "http://localhost:8080/api/v1/admin/sellers/$($app.id)/approve" -Headers $ah   # -> status approved

# 6. Wait for the dispatcher to deliver SellerApproved (governed by OUTBOX_POLL_INTERVAL), then re-login.
Start-Sleep -Seconds 3
$buyer2 = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/login -ContentType application/json `
  -Body (@{ email = "seller@example.com"; password = "password123" } | ConvertTo-Json)
$buyer2.user.role             # -> seller   (role flipped by the identity consumer)
$sh = @{ Authorization = "Bearer $($buyer2.tokens.access_token)" }

# 7. Edit the store as the now-seller
Invoke-RestMethod -Method Get   -Uri http://localhost:8080/api/v1/seller/store -Headers $sh
Invoke-RestMethod -Method Patch -Uri http://localhost:8080/api/v1/seller/store -Headers $sh -ContentType application/json `
  -Body (@{ name = "Acme Store"; contact = "acme@x.com"; description = "Best widgets" } | ConvertTo-Json)

# 8. Admin suspends -> store edits are then forbidden (403); role is unchanged
Invoke-RestMethod -Method Post -Uri "http://localhost:8080/api/v1/admin/sellers/$($app.id)/suspend" -Headers $ah
try {
  Invoke-RestMethod -Method Patch -Uri http://localhost:8080/api/v1/seller/store -Headers $sh -ContentType application/json `
    -Body (@{ name = "Nope"; contact = "x" } | ConvertTo-Json)
} catch { $_.Exception.Response.StatusCode.value__ }   # -> 403

# 9. The atomic-publish + cross-module guarantees, end to end
task test:integration

# 10. Tables exist with the seller_ prefix
docker compose exec postgres psql -U eco -d eco -c "\dt seller_*"
```

---

## 12. Handoff to P6 (Catalog: Categories, Products, Variants)

P6 builds on the seams P5 created — no rework:
- **`seller.Reader` is live:** P6 receives the seller service as `seller.Reader` (wire it in
  `cmd/api/main.go`) to gate product visibility on seller status — a product is discoverable only when its
  seller is `approved` (PRD FR-11). Resolve by **id via the port**, never a cross-module join.
- **`SellerSuspended` is published:** P6 **consumes** it (`bus.Subscribe(seller.EventSellerSuspended,
  events.Idempotent(pool, "catalog", …))`) to hide a suspended seller's products — the second cross-module
  consumer, now a proven pattern from P5's identity consumer.
- **The new-module template is proven twice:** P6 copies the same `domain/service/repo/handler/port` shape +
  a fifth sqlc target (`catalogdb`) + a sixth migration; products reference `seller_id` and `category_id`
  as **plain UUIDs**.
- **Admin-on-owning-module continues:** P6's product moderation/unpublish are `RequireRole("admin")` handlers
  on `catalog`, exactly as P5's approve/reject/suspend live on `seller`.
- **Role gating for seller writes:** P6's product create/update sit behind `RequireRole("seller")` **and** a
  status==`approved` check (via `seller.Reader`), mirroring P5's store-edit gate (a suspended seller cannot
  create products, FR-8).
```
