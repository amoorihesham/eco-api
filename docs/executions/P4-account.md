# Execution Plan — P4: Account: Profile & Addresses

| | |
|---|---|
| **Phase** | P4 — Account: Profile & Addresses (see [../IMPLEMENTATION_PLAN.md](../IMPLEMENTATION_PLAN.md)) |
| **Status** | Ready to implement |
| **Date** | 2026-06-16 |
| **Outcome** | A logged-in buyer reads and edits their profile (`GET/PATCH /api/v1/me`), and manages a shipping **address book** (`/api/v1/me/addresses` CRUD) with **exactly one default** address enforced at the DB and the service. Every `/me*` route is bearer-authenticated and **owner-scoped**: a user touching another user's address gets a **404** (no existence leak). P4 establishes the reusable **ownership / tenant-isolation** rule the rest of the system copies, and wires the `identity.Reader` port so P5 can resolve a user by ID. |
| **Module path** | `github.com/amoorihesham/eco-api` |

> This is an **execution document**: detailed enough to implement directly. Code blocks are working
> skeletons — type them in, adjust names to taste. Builds directly on **P3**
> ([P3-identity-auth.md](P3-identity-auth.md)) and **extends the same `identity` module** (the
> coverage matrix assigns *profile + addresses* to `identity`). Companion docs: [PRD](../PRD.md) ·
> [ARCHITECTURE](../ARCHITECTURE.md) · [OpenAPI](../../api/openapi.yaml).

---

## 1. Overview

**Objective.** Let users manage their profile and shipping addresses, and with it establish
**ownership / tenant isolation** — "a user touches only their own resources" — as a reusable rule
every later module inherits. P4 adds no new module: it grows the existing `internal/modules/identity`
with the OpenAPI **Account** tag (`/me`, `/me/addresses`), a fourth migration (`identity_addresses`),
and a tiny `internal/platform/auth` ownership helper.

**In scope**
- **Profile** read/update — `GET /api/v1/me` (current user) and `PATCH /api/v1/me` (update `name`),
  realizing the OpenAPI `getMe` / `updateMe` operations (schemas `User` / `UpdateUserInput`).
- **Address book CRUD** — `GET`/`POST /api/v1/me/addresses` and
  `GET`/`PATCH`/`DELETE /api/v1/me/addresses/{addressId}` (`Address` / `AddressInput`).
- **Default-address invariant** — **exactly one** default per user, enforced by a Postgres **partial
  unique index** *and* in the service (set-one clears the rest atomically; the first address added is
  default; deleting the default promotes the newest remaining one).
- **Ownership / tenant isolation** — all `/me*` routes sit behind `auth.Authn`; the caller's id comes
  from the **verified token** (`auth.UserID(ctx)`), never the body or path. Every address query is
  **owner-scoped** (`WHERE … AND user_id = $caller`), so a missing-or-foreign id collapses to
  `ErrAddressNotFound` → **404**. A reusable `auth.EnsureOwner` (→ `403`) is added for the
  shared-resource case P5+ needs.
- **`identity.Reader` port wired** — `*service.Service` gains `UserByID(ctx, id) → identity.PublicUser`
  so sibling modules (P5 seller) can resolve a user without importing `service`/`repo`/`domain`.
- Migration `000004_account` (`identity_addresses`) + new `identity.sql` queries; the `identitydb`
  sqlc target is **regenerated** (no new sqlc block — the queries dir already exists).

**Out of scope (later phases)**
- The **seller** role transition (`buyer → seller`) and store-profile management → **P5** (reads/writes
  the `role` column and the `identity.Reader` port established here).
- The **403 shared-ownership** *use* of `auth.EnsureOwner` (a seller editing only their own products) →
  **P5/P6**; P4 only ships the helper + its unit test.
- **Address use in checkout** (`shipping_address` selection, PRD FR-27) → **P11**.
- **Rate-limiting** and the **import-boundary lint gate** → **P18** (conventions now, enforced later).

**Depends on.** P3 (identity module, `auth` package, `httpx` envelope, outbox/`RunInTx`).

---

## 2. Prerequisites (Windows / PowerShell)

P4 adds **no new tools and no new Go dependencies** — everything (`golang-jwt`, `google/uuid`,
`pgx`, `sqlc`, `golang-migrate`) is already present from P0–P3.

| Need | Why | Command |
|---|---|---|
| Compose Postgres running | migrations + integration tests | `task db:up` |
| P3 migration applied | `identity_users` must exist (FK target) | `task migrate:up` → `task migrate:version` ⇒ `3` |
| `AUTH_JWT_SECRET` set | the server (and the `/me` Authn middleware) refuse to boot otherwise | already in `.env` from P3 |

> No `go get` in this phase. If `task migrate:version` is below `3`, finish P3 first.

---

## 3. Tech stack & versions

Unchanged from P3 — P4 reuses the established stack. The only **new code patterns** are listed for clarity.

| Concern | Choice (carried from P3 unless noted) |
|---|---|
| Transport | stdlib `net/http` `ServeMux`; **path params** via `r.PathValue("addressId")` (Go 1.22 mux) |
| Caller identity | `auth.Authn` → `auth.ClaimsFrom(ctx)`; **new** `auth.UserID(ctx)` convenience |
| Ownership (shared resource) | **new** `auth.EnsureOwner(caller, owner)` → `auth.ErrForbidden` (the 403 case) |
| Tenant isolation (collections) | **owner-scoped SQL** (`AND user_id = $caller`) → 404 on miss; defense-in-depth |
| Default-address invariant | Postgres **partial unique index** `WHERE is_default` + atomic clear-then-set in `db.RunInTx` |
| Optional address fields | `text NOT NULL DEFAULT ''`; DTO `json:"…,omitempty"` (no `pgtype` plumbing) |
| Queries / codegen | **sqlc** — append to the existing `identitydb` block; regenerate (no new target) |
| Atomicity | the P1 `db.RunInTx`; **no events** in P4, so no outbox writes |
| Unit-test mock | pure-Go fakes; white-box test for the default decision; DB-backed for the invariant |

> No outbox/event is produced in P4 (IMPLEMENTATION_PLAN: *Contracts & events — None new*). Multi-step
> writes (clear-default → insert, delete → promote) still use `db.RunInTx` purely for **atomicity**.

---

## 4. Target file tree (delta on P3)

```text
eco-api/
├── migrations/
│   ├── 000004_account.up.sql                         # NEW: identity_addresses + partial-unique default index
│   └── 000004_account.down.sql                       # NEW
├── internal/platform/auth/
│   ├── ownership.go                                  # NEW: UserID(ctx) + EnsureOwner + ErrForbidden
│   └── ownership_test.go                             # NEW: EnsureOwner allow/deny + UserID from ctx (no DB)
└── internal/modules/identity/
    ├── domain/
    │   ├── address.go                                # NEW: Address entity + field constants
    │   └── errors.go                                 # CHANGED: + ErrAddressNotFound, ErrUserNotFound
    ├── service/
    │   ├── ports.go                                  # CHANGED: + address repo methods + UpdateUserName
    │   ├── account.go                                # NEW: profile + address use cases (default invariant)
    │   ├── reader.go                                 # NEW: UserByID → identity.PublicUser (satisfies port)
    │   ├── account_test.go                           # NEW (package service): wantDefault truth table (no DB)
    │   └── service_test.go                           # CHANGED: fakeRepo gains the new no-op methods
    ├── repo/
    │   ├── repo.go                                   # CHANGED: + address + profile adapter methods
    │   ├── queries/identity.sql                      # CHANGED: + address + UpdateUserName queries
    │   ├── identitydb/                               # REGENERATED + committed (task sqlc)
    │   └── account_integration_test.go               # NEW //go:build integration: CRUD + default + cross-user 404
    └── handler/
        ├── account.go                                # NEW: /me + /me/addresses handlers + DTOs
        └── routes.go                                 # CHANGED: mount the Account routes behind Authn
```

**Not changed:** `cmd/api/main.go` — the Account routes ride the existing `identityH.Mount(mux,
auth.Authn(jwt))` call (P3 already passes the `Authn` middleware). `port.go` is unchanged: the `Reader`
interface and `PublicUser` were declared in P3; P4 only adds the method that satisfies them.

**Import-direction rule (carried from P3):** `identity/domain` and `identity/service` stay pure —
stdlib, `google/uuid`, the module's own `domain`, the module's root `identity` package (for
`PublicUser`), and **ports** (`platform/auth`, `platform/db`). `service` may name `pgx.Tx`/`db.Beginner`
as the unit-of-work currency (the P1 §6 allow-listed exception). Only `repo/` imports `pgx` driver code.

---

## 5. Execution steps

Work top to bottom; each step ends in a check. Assumes P3 is complete (`identity` module, `auth`
package, `httpx`, `db.RunInTx`, compose Postgres at migration version `3`).

### S1 — Migration: `identity_addresses`
```powershell
task migrate:new -- account    # creates migrations/000004_account.{up,down}.sql
```
Fill the up/down files (full contents in §8): `identity_addresses` (identity_ prefix; in-module FK to
`identity_users`), a per-user index, and the **partial unique index** that enforces one default. Then:

```powershell
task migrate:up
task migrate:version           # -> 4
```
**Check:** `migrate:version` prints `4` (not dirty); `identity_addresses` exists with index
`uq_identity_addresses_one_default`.

### S2 — sqlc: address + profile queries
Append the new queries to `internal/modules/identity/repo/queries/identity.sql` (full in §8). **No new
sqlc block** — the `identitydb` target already covers this dir. Then:

```powershell
task sqlc                       # regenerates internal/modules/identity/repo/identitydb/*
```
**Check:** `go build ./internal/modules/identity/repo/identitydb`; `task sqlc:check` clean after commit.

### S3 — Ownership helper (`platform/auth`)
Create `internal/platform/auth/ownership.go` (full in §8): `UserID(ctx)`, `EnsureOwner(caller, owner)`,
and the `ErrForbidden` sentinel.
**Check:** `go build ./internal/platform/auth`.

### S4 — Domain: Address + errors
Create `internal/modules/identity/domain/address.go` and extend `errors.go` (full in §8): the `Address`
value object and the `ErrAddressNotFound` / `ErrUserNotFound` sentinels. Pure Go.
**Check:** `go build ./internal/modules/identity/domain`.

### S5 — Service ports + use cases + Reader
Extend `service/ports.go` (address methods + `UpdateUserName`) and add `service/account.go`
(profile + address use cases, default invariant) and `service/reader.go` (`UserByID`). Full in §8.
**Check:** `go build ./internal/modules/identity/service`.

### S6 — Repo adapter
Extend `internal/modules/identity/repo/repo.go` with the address + profile methods mapping `identitydb`
rows ↔ `domain.Address` (full in §8); writes take `pgx.Tx`, reads take `ctx` (P3 convention).
**Check:** `go build ./internal/modules/identity/repo`.

### S7 — Handler + routes
Create `handler/account.go` (the `/me` + `/me/addresses` handlers, DTOs, validation) and extend
`handler/routes.go` to mount them behind `Authn` (full in §8).
**Check:** `go build ./internal/modules/identity/...`.

### S8 — Run + tests
`cmd/api/main.go` needs no change (routes ride the existing `Mount`). Build, run, exercise.
```powershell
task run                        # boots; GET /api/v1/me with a P3 access token -> 200
task test                       # unit: ownership, wantDefault, config — Docker-free
task test:integration           # address CRUD + default invariant + cross-user 404
task ci                         # tidy -> sqlc generate -> lint -> test -> build -> green
```
**Check:** `task ci` green; both suites pass.

---

## 6. The ownership & tenant-isolation contract

This is the discipline P4 establishes — the analog of P0's response envelope, P1's table-ownership rule,
P2's outbox contract, and P3's auth/module template. It realizes ARCHITECTURE §10 (security: *RBAC +
ownership checks*) and §6's privacy NFR (*buyer PII reachable only by owner/admin*; PRD §7).

**Two distinct shapes — pick by resource kind.**

1. **Tenant-scoped collection** (the address book, the P4 case). The caller can only ever address *its
   own* rows, so the right answer is **owner-scoped queries**: every statement carries
   `AND user_id = $caller`, and "row missing" and "row owned by someone else" are **indistinguishable** —
   both return zero rows → the service maps that to `ErrAddressNotFound` → **404**. This is defense in
   depth (the DB never even returns a foreign row) and it **does not leak existence** (a 403 would
   confirm the id is real). *Never* trust an id from the path for ownership; trust only `user_id` from
   the verified token.

2. **Shared resource with an owner column** (a seller's product in P6, a store in P5). Here the resource
   is fetched, then guarded: `auth.EnsureOwner(callerID, resource.OwnerID)` → `ErrForbidden` → **403**.
   P4 ships the helper and its unit test; the first *use* is P5+.

**Caller identity comes from the token, full stop.**
- `/me*` routes are wrapped with `auth.Authn` (P3) at mount time; the handler reads
  `auth.UserID(r.Context())` (a thin wrapper over `auth.ClaimsFrom`). The request **body** and **path**
  never carry the acting user id.

**The default-address invariant (exactly one default per user).**
- Enforced **twice**: a Postgres **partial unique index** `… (user_id) WHERE is_default` makes a second
  default physically impossible; the service keeps it *true-by-one* by **clearing then setting** inside a
  single `db.RunInTx` (clear first to avoid tripping the index).
- **Create:** the **first** address a user adds is forced `is_default = true`; any later address with
  `is_default = true` demotes the others.
- **Update:** promoting an address to default demotes the rest; you **cannot** directly clear the only
  default (you move it by promoting another) — so the count never drops to zero while addresses exist.
- **Delete:** deleting the default **promotes the newest remaining** address; deleting the last address
  leaves none (zero is valid only when the book is empty).

| Concern | Type / helper |
|---|---|
| Read caller id | `auth.UserID(ctx)` → `(uuid.UUID, bool)` |
| Guard a shared resource (403) | `auth.EnsureOwner(callerID, ownerID)` → `auth.ErrForbidden` |
| Isolate a collection (404) | owner-scoped query `… AND user_id = $caller`; map no-rows → `domain.ErrAddressNotFound` |
| Atomic clear-then-set default | `db.RunInTx` → `ClearDefaultAddresses` then `Insert/SetAddressDefault` |
| Resolve a user for siblings | `identity.Reader.UserByID(ctx, id)` → `identity.PublicUser` (no password hash crosses) |

**Contracts & events (from IMPLEMENTATION_PLAN).** A platform-wide ownership-enforcement helper.
**No domain events** are published or consumed in P4.

---

## 7. Configuration reference (additions to P3)

**None.** P4 introduces no new environment variables. All P0–P3 settings (`HTTP_*`, `LOG_*`,
`ENVIRONMENT`, `DATABASE_URL`, `DB_*`, `OUTBOX_*`, `AUTH_*`) are unchanged and sufficient.

---

## 8. Full file contents

**`migrations/000004_account.up.sql`**
```sql
-- P4 Account: the buyer shipping-address book (identity_ prefix; P1 ownership rule).
-- In-module FK to identity_users is allowed; no cross-module FK. Optional fields are
-- NOT NULL DEFAULT '' so generated Go stays plain strings (the API omits empties).

CREATE TABLE identity_addresses (
    id          uuid        PRIMARY KEY,
    user_id     uuid        NOT NULL REFERENCES identity_users(id) ON DELETE CASCADE,
    recipient   text        NOT NULL,
    line1       text        NOT NULL,
    line2       text        NOT NULL DEFAULT '',
    city        text        NOT NULL,
    region      text        NOT NULL DEFAULT '',
    postal_code text        NOT NULL,
    country     text        NOT NULL,                 -- ISO 3166-1 alpha-2 (validated in the handler)
    phone       text        NOT NULL DEFAULT '',
    is_default  boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_identity_addresses_user ON identity_addresses (user_id);

-- The default-address invariant: at most one default row per user (PRD FR-1).
CREATE UNIQUE INDEX uq_identity_addresses_one_default
    ON identity_addresses (user_id) WHERE is_default;
```

**`migrations/000004_account.down.sql`**
```sql
DROP TABLE IF EXISTS identity_addresses;
```

**`internal/modules/identity/repo/queries/identity.sql`** — append these queries (keep the P3 ones):
```sql
-- name: UpdateUserName :one
UPDATE identity_users SET name = $2 WHERE id = $1
RETURNING id, email, password_hash, name, role, created_at;

-- name: ListAddresses :many
SELECT id, user_id, recipient, line1, line2, city, region, postal_code, country, phone, is_default, created_at
FROM identity_addresses
WHERE user_id = $1
ORDER BY is_default DESC, created_at DESC;

-- name: GetAddress :one
SELECT id, user_id, recipient, line1, line2, city, region, postal_code, country, phone, is_default, created_at
FROM identity_addresses
WHERE id = $1 AND user_id = $2;

-- name: CountAddresses :one
SELECT count(*) FROM identity_addresses WHERE user_id = $1;

-- name: InsertAddress :exec
INSERT INTO identity_addresses
    (id, user_id, recipient, line1, line2, city, region, postal_code, country, phone, is_default)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: UpdateAddress :exec
UPDATE identity_addresses
SET recipient = $3, line1 = $4, line2 = $5, city = $6, region = $7,
    postal_code = $8, country = $9, phone = $10, is_default = $11
WHERE id = $1 AND user_id = $2;

-- name: DeleteAddress :execrows
DELETE FROM identity_addresses WHERE id = $1 AND user_id = $2;

-- name: ClearDefaultAddresses :exec
UPDATE identity_addresses SET is_default = false WHERE user_id = $1 AND is_default;

-- name: SetAddressDefault :exec
UPDATE identity_addresses SET is_default = true WHERE id = $1 AND user_id = $2;

-- name: NewestAddressID :one
SELECT id FROM identity_addresses WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1;
```

> **sqlc output names** (from `task sqlc`): a new model `IdentityAddress`; param structs
> `InsertAddressParams`, `UpdateAddressParams`, `GetAddressParams{ID, UserID}`,
> `DeleteAddressParams{ID, UserID}`, `SetAddressDefaultParams{ID, UserID}`, `UpdateUserNameParams{ID, Name}`.
> `ListAddresses`/`GetAddress` select all columns so they return the full `IdentityAddress` model;
> `CountAddresses` returns `int64`; `NewestAddressID` returns `uuid.UUID`; `UpdateUserName` returns the
> `IdentityUser` model.

**`internal/platform/auth/ownership.go`**
```go
package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrForbidden is returned by EnsureOwner when the caller does not own a shared resource.
// Map it to httpx 403 in handlers (the 403 case — distinct from tenant-scoped 404; see P4 §6).
var ErrForbidden = errors.New("forbidden")

// UserID returns the authenticated caller's id from the request context (placed by Authn).
// ok is false when the request was not authenticated.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	c, ok := ClaimsFrom(ctx)
	if !ok {
		return uuid.Nil, false
	}
	return c.UserID, true
}

// EnsureOwner enforces that the caller owns a shared, fetched resource. Use this for resources that
// are addressable across users (a seller's product/store, P5+). For collections that are entirely
// private to the caller (the address book), prefer owner-scoped queries that return 404 on a miss.
func EnsureOwner(callerID, ownerID uuid.UUID) error {
	if callerID != ownerID {
		return ErrForbidden
	}
	return nil
}
```

**`internal/modules/identity/domain/address.go`**
```go
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Address is a buyer's saved shipping address. Optional fields (Line2, Region, Phone) are the empty
// string when absent. Exactly one Address per user may carry IsDefault (enforced in service + DB).
type Address struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Recipient  string
	Line1      string
	Line2      string
	City       string
	Region     string
	PostalCode string
	Country    string // ISO 3166-1 alpha-2
	Phone      string
	IsDefault  bool
	CreatedAt  time.Time
}
```

**`internal/modules/identity/domain/errors.go`** (CHANGED — add the two sentinels)
```go
package domain

import "errors"

var (
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid or expired token")

	// P4 — Account.
	ErrUserNotFound    = errors.New("user not found")
	ErrAddressNotFound = errors.New("address not found") // also returned for a foreign address (no leak)
)
```

**`internal/modules/identity/service/ports.go`** (CHANGED — extend `Repository`; `Outbox` unchanged)
```go
package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

// Repository is the persistence port the service needs; repo/ implements it over sqlc.
// Write methods take pgx.Tx so the service composes them in one RunInTx; read methods take ctx and
// return pgx.ErrNoRows when absent (the service maps that to domain errors). All address methods are
// owner-scoped (they carry user_id) — P4 §6 tenant isolation.
type Repository interface {
	// --- identity (P3) ---
	CreateUser(ctx context.Context, tx pgx.Tx, u domain.User) error
	GetUserByEmail(ctx context.Context, email string) (domain.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	UpdatePasswordHash(ctx context.Context, tx pgx.Tx, userID uuid.UUID, hash string) error

	InsertRefreshToken(ctx context.Context, tx pgx.Tx, rt domain.RefreshToken) error
	GetRefreshToken(ctx context.Context, tokenHash string) (domain.RefreshToken, error)
	DeleteRefreshToken(ctx context.Context, tx pgx.Tx, tokenHash string) error
	DeleteUserRefreshTokens(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error

	InsertPasswordReset(ctx context.Context, tx pgx.Tx, pr domain.PasswordReset) error
	GetActivePasswordReset(ctx context.Context, tokenHash string) (domain.PasswordReset, error)
	MarkPasswordResetUsed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error

	// --- account (P4) ---
	UpdateUserName(ctx context.Context, tx pgx.Tx, userID uuid.UUID, name string) (domain.User, error)

	ListAddresses(ctx context.Context, userID uuid.UUID) ([]domain.Address, error)
	GetAddress(ctx context.Context, userID, id uuid.UUID) (domain.Address, error)
	CountAddresses(ctx context.Context, userID uuid.UUID) (int, error)
	InsertAddress(ctx context.Context, tx pgx.Tx, a domain.Address) error
	UpdateAddress(ctx context.Context, tx pgx.Tx, a domain.Address) error
	DeleteAddress(ctx context.Context, tx pgx.Tx, userID, id uuid.UUID) (int64, error)
	ClearDefaultAddresses(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error
	SetAddressDefault(ctx context.Context, tx pgx.Tx, userID, id uuid.UUID) error
	NewestAddressID(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (uuid.UUID, error)
}

// Outbox is the publish port (satisfied by *events.Outbox) — kept narrow for testability.
type Outbox interface {
	Write(ctx context.Context, tx pgx.Tx, e events.Event) error
}
```

**`internal/modules/identity/service/account.go`** (NEW)
```go
package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/platform/db"
)

// AddressInput carries the editable fields of an address (id/user/created_at are server-owned).
type AddressInput struct {
	Recipient  string
	Line1      string
	Line2      string
	City       string
	Region     string
	PostalCode string
	Country    string
	Phone      string
	IsDefault  bool
}

// GetProfile returns the current user. Maps a missing row to ErrUserNotFound.
func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (domain.User, error) {
	u, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, domain.ErrUserNotFound
		}
		return domain.User{}, err
	}
	return u, nil
}

// UpdateProfile updates the user's display name and returns the fresh row.
func (s *Service) UpdateProfile(ctx context.Context, userID uuid.UUID, name string) (domain.User, error) {
	var u domain.User
	err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		u, err = s.repo.UpdateUserName(ctx, tx, userID, name)
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, domain.ErrUserNotFound
		}
		return domain.User{}, err
	}
	return u, nil
}

// ListAddresses returns the caller's addresses, default first.
func (s *Service) ListAddresses(ctx context.Context, userID uuid.UUID) ([]domain.Address, error) {
	return s.repo.ListAddresses(ctx, userID)
}

// GetAddress returns one owner-scoped address; a missing-or-foreign id yields ErrAddressNotFound.
func (s *Service) GetAddress(ctx context.Context, userID, addressID uuid.UUID) (domain.Address, error) {
	a, err := s.repo.GetAddress(ctx, userID, addressID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Address{}, domain.ErrAddressNotFound
		}
		return domain.Address{}, err
	}
	return a, nil
}

// CreateAddress adds an address. The first address is forced default; a requested default demotes the
// rest. Clear-then-insert run in one tx so the partial-unique index is never tripped.
func (s *Service) CreateAddress(ctx context.Context, userID uuid.UUID, in AddressInput) (domain.Address, error) {
	count, err := s.repo.CountAddresses(ctx, userID)
	if err != nil {
		return domain.Address{}, err
	}
	makeDefault := wantDefault(count, in.IsDefault)

	a := domain.Address{
		ID:         uuid.New(),
		UserID:     userID,
		Recipient:  in.Recipient,
		Line1:      in.Line1,
		Line2:      in.Line2,
		City:       in.City,
		Region:     in.Region,
		PostalCode: in.PostalCode,
		Country:    in.Country,
		Phone:      in.Phone,
		IsDefault:  makeDefault,
		CreatedAt:  time.Now().UTC(),
	}
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if makeDefault {
			if err := s.repo.ClearDefaultAddresses(ctx, tx, userID); err != nil {
				return err
			}
		}
		return s.repo.InsertAddress(ctx, tx, a)
	})
	if err != nil {
		return domain.Address{}, err
	}
	return a, nil
}

// UpdateAddress edits an owner-scoped address. Promoting to default demotes the rest; an address that
// is already default stays default (you move the default by promoting another, never by clearing it).
func (s *Service) UpdateAddress(ctx context.Context, userID, addressID uuid.UUID, in AddressInput) (domain.Address, error) {
	existing, err := s.GetAddress(ctx, userID, addressID) // 404 if missing/foreign
	if err != nil {
		return domain.Address{}, err
	}
	makeDefault := existing.IsDefault || in.IsDefault

	updated := domain.Address{
		ID:         existing.ID,
		UserID:     userID,
		Recipient:  in.Recipient,
		Line1:      in.Line1,
		Line2:      in.Line2,
		City:       in.City,
		Region:     in.Region,
		PostalCode: in.PostalCode,
		Country:    in.Country,
		Phone:      in.Phone,
		IsDefault:  makeDefault,
		CreatedAt:  existing.CreatedAt,
	}
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if in.IsDefault && !existing.IsDefault {
			if err := s.repo.ClearDefaultAddresses(ctx, tx, userID); err != nil {
				return err
			}
		}
		return s.repo.UpdateAddress(ctx, tx, updated)
	})
	if err != nil {
		return domain.Address{}, err
	}
	return updated, nil
}

// DeleteAddress removes an owner-scoped address. Deleting the default promotes the newest remaining one
// so the invariant (one default while any address exists) holds.
func (s *Service) DeleteAddress(ctx context.Context, userID, addressID uuid.UUID) error {
	existing, err := s.GetAddress(ctx, userID, addressID) // 404 if missing/foreign
	if err != nil {
		return err
	}
	return db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := s.repo.DeleteAddress(ctx, tx, userID, addressID)
		if err != nil {
			return err
		}
		if rows == 0 {
			return domain.ErrAddressNotFound
		}
		if !existing.IsDefault {
			return nil
		}
		newest, err := s.repo.NewestAddressID(ctx, tx, userID) // same tx: sees the delete
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // book is now empty — zero defaults is valid
			}
			return err
		}
		return s.repo.SetAddressDefault(ctx, tx, userID, newest)
	})
}

// wantDefault reports whether a new address should be the default: the first one always is, or one the
// caller explicitly requests.
func wantDefault(existingCount int, requested bool) bool {
	return requested || existingCount == 0
}
```

**`internal/modules/identity/service/reader.go`** (NEW — satisfies `identity.Reader`)
```go
package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "github.com/amoorihesham/eco-api/internal/modules/identity"
	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
)

// UserByID resolves a user for sibling modules via the identity.Reader port (P5 seller wires it).
// Only the public projection crosses the boundary — never the password hash.
func (s *Service) UserByID(ctx context.Context, id uuid.UUID) (identity.PublicUser, error) {
	u, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return identity.PublicUser{}, domain.ErrUserNotFound
		}
		return identity.PublicUser{}, err
	}
	return identity.PublicUser{ID: u.ID, Email: u.Email, Name: u.Name, Role: string(u.Role)}, nil
}
```

**`internal/modules/identity/repo/repo.go`** (CHANGED — append these methods; P3 methods unchanged)
```go
// --- account (P4) ---

func (r *Repo) UpdateUserName(ctx context.Context, tx pgx.Tx, userID uuid.UUID, name string) (domain.User, error) {
	row, err := r.q.WithTx(tx).UpdateUserName(ctx, identitydb.UpdateUserNameParams{ID: userID, Name: name})
	if err != nil {
		return domain.User{}, err
	}
	return toUser(row.ID, row.Email, row.PasswordHash, row.Name, row.Role, row.CreatedAt), nil
}

func (r *Repo) ListAddresses(ctx context.Context, userID uuid.UUID) ([]domain.Address, error) {
	rows, err := r.q.ListAddresses(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Address, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAddress(row))
	}
	return out, nil
}

func (r *Repo) GetAddress(ctx context.Context, userID, id uuid.UUID) (domain.Address, error) {
	row, err := r.q.GetAddress(ctx, identitydb.GetAddressParams{ID: id, UserID: userID})
	if err != nil {
		return domain.Address{}, err
	}
	return toAddress(row), nil
}

func (r *Repo) CountAddresses(ctx context.Context, userID uuid.UUID) (int, error) {
	n, err := r.q.CountAddresses(ctx, userID)
	return int(n), err
}

func (r *Repo) InsertAddress(ctx context.Context, tx pgx.Tx, a domain.Address) error {
	return r.q.WithTx(tx).InsertAddress(ctx, identitydb.InsertAddressParams{
		ID:         a.ID,
		UserID:     a.UserID,
		Recipient:  a.Recipient,
		Line1:      a.Line1,
		Line2:      a.Line2,
		City:       a.City,
		Region:     a.Region,
		PostalCode: a.PostalCode,
		Country:    a.Country,
		Phone:      a.Phone,
		IsDefault:  a.IsDefault,
	})
}

func (r *Repo) UpdateAddress(ctx context.Context, tx pgx.Tx, a domain.Address) error {
	return r.q.WithTx(tx).UpdateAddress(ctx, identitydb.UpdateAddressParams{
		ID:         a.ID,
		UserID:     a.UserID,
		Recipient:  a.Recipient,
		Line1:      a.Line1,
		Line2:      a.Line2,
		City:       a.City,
		Region:     a.Region,
		PostalCode: a.PostalCode,
		Country:    a.Country,
		Phone:      a.Phone,
		IsDefault:  a.IsDefault,
	})
}

func (r *Repo) DeleteAddress(ctx context.Context, tx pgx.Tx, userID, id uuid.UUID) (int64, error) {
	return r.q.WithTx(tx).DeleteAddress(ctx, identitydb.DeleteAddressParams{ID: id, UserID: userID})
}

func (r *Repo) ClearDefaultAddresses(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	return r.q.WithTx(tx).ClearDefaultAddresses(ctx, userID)
}

func (r *Repo) SetAddressDefault(ctx context.Context, tx pgx.Tx, userID, id uuid.UUID) error {
	return r.q.WithTx(tx).SetAddressDefault(ctx, identitydb.SetAddressDefaultParams{ID: id, UserID: userID})
}

func (r *Repo) NewestAddressID(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (uuid.UUID, error) {
	return r.q.WithTx(tx).NewestAddressID(ctx, userID)
}

func toAddress(row identitydb.IdentityAddress) domain.Address {
	return domain.Address{
		ID:         row.ID,
		UserID:     row.UserID,
		Recipient:  row.Recipient,
		Line1:      row.Line1,
		Line2:      row.Line2,
		City:       row.City,
		Region:     row.Region,
		PostalCode: row.PostalCode,
		Country:    row.Country,
		Phone:      row.Phone,
		IsDefault:  row.IsDefault,
		CreatedAt:  row.CreatedAt,
	}
}
```

**`internal/modules/identity/handler/account.go`** (NEW)
```go
package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/modules/identity/service"
	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// --- DTOs (mirror the OpenAPI Account schemas) ---

type updateMeRequest struct {
	Name string `json:"name"`
}

type addressInput struct {
	Recipient  string `json:"recipient"`
	Line1      string `json:"line1"`
	Line2      string `json:"line2"`
	City       string `json:"city"`
	Region     string `json:"region"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
	Phone      string `json:"phone"`
	IsDefault  bool   `json:"is_default"`
}

type addressDTO struct {
	ID         string `json:"id"`
	Recipient  string `json:"recipient"`
	Line1      string `json:"line1"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
	Phone      string `json:"phone,omitempty"`
	IsDefault  bool   `json:"is_default"`
}

type addressListResponse struct {
	Data []addressDTO `json:"data"`
}

// --- profile ---

func (h *Handler) getMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	u, err := h.svc.GetProfile(r.Context(), userID)
	if err != nil {
		writeAccountError(w, err, "could not load profile")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toUserDTO(u))
}

func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	var req updateMeRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed",
			httpx.ErrorDetail{Field: "name", Message: "name is required"})
		return
	}
	u, err := h.svc.UpdateProfile(r.Context(), userID, strings.TrimSpace(req.Name))
	if err != nil {
		writeAccountError(w, err, "could not update profile")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toUserDTO(u))
}

// --- address book ---

func (h *Handler) listAddresses(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	addrs, err := h.svc.ListAddresses(r.Context(), userID)
	if err != nil {
		httpx.Internal(w, "could not list addresses")
		return
	}
	out := make([]addressDTO, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, toAddressDTO(a))
	}
	httpx.WriteJSON(w, http.StatusOK, addressListResponse{Data: out})
}

func (h *Handler) createAddress(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	var req addressInput
	if !decode(w, r, &req) {
		return
	}
	if errs := validateAddress(req); len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	a, err := h.svc.CreateAddress(r.Context(), userID, toAddressInput(req))
	if err != nil {
		httpx.Internal(w, "could not create address")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toAddressDTO(a))
}

func (h *Handler) getAddress(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	addressID, ok := pathAddressID(w, r)
	if !ok {
		return
	}
	a, err := h.svc.GetAddress(r.Context(), userID, addressID)
	if err != nil {
		writeAccountError(w, err, "could not load address")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAddressDTO(a))
}

func (h *Handler) updateAddress(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	addressID, ok := pathAddressID(w, r)
	if !ok {
		return
	}
	var req addressInput
	if !decode(w, r, &req) {
		return
	}
	if errs := validateAddress(req); len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	a, err := h.svc.UpdateAddress(r.Context(), userID, addressID, toAddressInput(req))
	if err != nil {
		writeAccountError(w, err, "could not update address")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAddressDTO(a))
}

func (h *Handler) deleteAddress(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(w, r)
	if !ok {
		return
	}
	addressID, ok := pathAddressID(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteAddress(r.Context(), userID, addressID); err != nil {
		writeAccountError(w, err, "could not delete address")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

// callerID reads the authenticated user id placed by Authn; writes 401 when absent.
func callerID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := auth.UserID(r.Context())
	if !ok {
		httpx.Unauthorized(w, "authentication required")
		return uuid.Nil, false
	}
	return id, true
}

// pathAddressID parses {addressId}; a malformed id is treated as not found (no existence leak).
func pathAddressID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("addressId"))
	if err != nil {
		httpx.NotFound(w, "address not found")
		return uuid.Nil, false
	}
	return id, true
}

// writeAccountError maps domain sentinels to the standard envelope; not-found becomes 404.
func writeAccountError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, domain.ErrAddressNotFound), errors.Is(err, domain.ErrUserNotFound):
		httpx.NotFound(w, "not found")
	default:
		httpx.Internal(w, fallback)
	}
}

func validateAddress(req addressInput) []httpx.ErrorDetail {
	var errs []httpx.ErrorDetail
	require := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, httpx.ErrorDetail{Field: field, Message: field + " is required"})
		}
	}
	require("recipient", req.Recipient)
	require("line1", req.Line1)
	require("city", req.City)
	require("postal_code", req.PostalCode)
	if len(strings.TrimSpace(req.Country)) != 2 {
		errs = append(errs, httpx.ErrorDetail{Field: "country", Message: "country must be a 2-letter ISO code"})
	}
	return errs
}

func toAddressInput(req addressInput) service.AddressInput {
	return service.AddressInput{
		Recipient:  strings.TrimSpace(req.Recipient),
		Line1:      strings.TrimSpace(req.Line1),
		Line2:      strings.TrimSpace(req.Line2),
		City:       strings.TrimSpace(req.City),
		Region:     strings.TrimSpace(req.Region),
		PostalCode: strings.TrimSpace(req.PostalCode),
		Country:    strings.ToUpper(strings.TrimSpace(req.Country)),
		Phone:      strings.TrimSpace(req.Phone),
		IsDefault:  req.IsDefault,
	}
}

func toAddressDTO(a domain.Address) addressDTO {
	return addressDTO{
		ID:         a.ID.String(),
		Recipient:  a.Recipient,
		Line1:      a.Line1,
		Line2:      a.Line2,
		City:       a.City,
		Region:     a.Region,
		PostalCode: a.PostalCode,
		Country:    a.Country,
		Phone:      a.Phone,
		IsDefault:  a.IsDefault,
	}
}

func toUserDTO(u domain.User) userDTO {
	return userDTO{
		ID:        u.ID.String(),
		Email:     u.Email,
		Name:      u.Name,
		Role:      string(u.Role),
		CreatedAt: u.CreatedAt.Format(time.RFC3339),
	}
}
```

> `userDTO` and the `decode` helper are defined in P3's `handler/handler.go` (same package); P4 reuses
> them and adds `toUserDTO`. Remove the now-duplicated inline `userDTO` construction from
> `toAuthResponse` if you prefer it to call `toUserDTO` — optional cleanup, not required.

**`internal/modules/identity/handler/routes.go`** (CHANGED — add the Account routes behind Authn)
```go
package handler

import (
	"net/http"

	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// Mount registers the identity routes under /api/v1. Auth endpoints are public except logout; every
// Account (/me*) route requires a valid access token, so it is wrapped with the Authn middleware
// supplied by the composition root.
func (h *Handler) Mount(mux *http.ServeMux, authn httpx.Middleware) {
	// Authentication (P3)
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.refresh)
	mux.Handle("POST /api/v1/auth/logout", authn(http.HandlerFunc(h.logout)))
	mux.HandleFunc("POST /api/v1/auth/password/forgot", h.forgotPassword)
	mux.HandleFunc("POST /api/v1/auth/password/reset", h.resetPassword)

	// Account (P4) — all behind Authn (the caller id comes from the verified token).
	mux.Handle("GET /api/v1/me", authn(http.HandlerFunc(h.getMe)))
	mux.Handle("PATCH /api/v1/me", authn(http.HandlerFunc(h.updateMe)))
	mux.Handle("GET /api/v1/me/addresses", authn(http.HandlerFunc(h.listAddresses)))
	mux.Handle("POST /api/v1/me/addresses", authn(http.HandlerFunc(h.createAddress)))
	mux.Handle("GET /api/v1/me/addresses/{addressId}", authn(http.HandlerFunc(h.getAddress)))
	mux.Handle("PATCH /api/v1/me/addresses/{addressId}", authn(http.HandlerFunc(h.updateAddress)))
	mux.Handle("DELETE /api/v1/me/addresses/{addressId}", authn(http.HandlerFunc(h.deleteAddress)))
}
```

**`internal/modules/identity/service/service_test.go`** (CHANGED — extend `fakeRepo` so it still
satisfies the grown `Repository`; append these no-op methods):
```go
// --- account (P4) no-ops, so fakeRepo still satisfies service.Repository ---

func (fakeRepo) UpdateUserName(context.Context, pgx.Tx, uuid.UUID, string) (domain.User, error) {
	return domain.User{}, nil
}
func (fakeRepo) ListAddresses(context.Context, uuid.UUID) ([]domain.Address, error) { return nil, nil }
func (fakeRepo) GetAddress(context.Context, uuid.UUID, uuid.UUID) (domain.Address, error) {
	return domain.Address{}, pgx.ErrNoRows
}
func (fakeRepo) CountAddresses(context.Context, uuid.UUID) (int, error)              { return 0, nil }
func (fakeRepo) InsertAddress(context.Context, pgx.Tx, domain.Address) error         { return nil }
func (fakeRepo) UpdateAddress(context.Context, pgx.Tx, domain.Address) error         { return nil }
func (fakeRepo) DeleteAddress(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (int64, error) {
	return 0, nil
}
func (fakeRepo) ClearDefaultAddresses(context.Context, pgx.Tx, uuid.UUID) error { return nil }
func (fakeRepo) SetAddressDefault(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return nil
}
func (fakeRepo) NewestAddressID(context.Context, pgx.Tx, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, pgx.ErrNoRows
}
```

---

## 9. Testing plan

| Test | File | Asserts | Needs DB? |
|---|---|---|---|
| `EnsureOwner` + `UserID` | `auth/ownership_test.go` | same id → nil; different id → `ErrForbidden`; `UserID` reads claims, false when absent | no |
| Default decision | `identity/service/account_test.go` | `wantDefault`: first address (count 0) → true; later non-default → false; explicit request → true | no |
| fakeRepo still compiles | `identity/service/service_test.go` | the P3 credential tests keep passing with the grown interface | no |
| Address CRUD + default invariant | `identity/repo/account_integration_test.go` | create→list→get→update→delete; first is default; promoting demotes; deleting default promotes newest | yes |
| Cross-user isolation | `identity/repo/account_integration_test.go` | user B's `GetAddress` of user A's id → `ErrAddressNotFound`; never another user's row | yes |
| Profile update | `identity/repo/account_integration_test.go` | `UpdateProfile` changes the name and returns it | yes |

**`internal/platform/auth/ownership_test.go`** (no Docker)
```go
package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/platform/auth"
)

func TestEnsureOwner(t *testing.T) {
	owner := uuid.New()
	if err := auth.EnsureOwner(owner, owner); err != nil {
		t.Fatalf("same id should be allowed, got %v", err)
	}
	if err := auth.EnsureOwner(uuid.New(), owner); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("want ErrForbidden for a different caller, got %v", err)
	}
}

func TestUserIDFromContext(t *testing.T) {
	if _, ok := auth.UserID(context.Background()); ok {
		t.Fatal("expected ok=false for an unauthenticated context")
	}
}
```

**`internal/modules/identity/service/account_test.go`** (no Docker — white-box, tests the unexported decision)
```go
package service

import "testing"

func TestWantDefault(t *testing.T) {
	cases := []struct {
		name      string
		count     int
		requested bool
		want      bool
	}{
		{"first address is forced default", 0, false, true},
		{"later address, not requested", 2, false, false},
		{"later address, explicitly requested", 2, true, true},
		{"first address, also requested", 0, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wantDefault(c.count, c.requested); got != c.want {
				t.Fatalf("wantDefault(%d, %v) = %v, want %v", c.count, c.requested, got, c.want)
			}
		})
	}
}
```

**`internal/modules/identity/repo/account_integration_test.go`** (build-tagged; against compose Postgres)
```go
//go:build integration

package repo_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

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

func assertSingleDefault(t *testing.T, pool *pgxpool.Pool, ctx context.Context, uid, want any) {
	t.Helper()
	var count int
	var defaultID any
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
```

Run: `task test` (unit) and `task test:integration` (DB-backed).

---

## 10. Definition of Done

- [ ] `task migrate:up` applies cleanly; `task migrate:version` → `4` (not dirty); `identity_addresses`
      exists with the `uq_identity_addresses_one_default` partial unique index.
- [ ] `task sqlc` regenerates `internal/modules/identity/repo/identitydb/*` (new `IdentityAddress`
      model + param structs); `task sqlc:check` reports no diff.
- [ ] `task run` boots; with a P3 access token `GET /api/v1/me` → `200` with the `User` body, and
      `PATCH /api/v1/me {"name":"…"}` → `200` with the updated name.
- [ ] Address CRUD works: `POST /me/addresses` → `201`; the **first** address comes back
      `is_default: true`; `GET /me/addresses` lists default-first; `PATCH`/`DELETE` work.
- [ ] **Exactly one default:** adding/promoting a default demotes the rest; deleting the default
      promotes the newest remaining; a second concurrent default is rejected by the DB index.
- [ ] **Ownership:** a user **cannot** read, update, or delete another user's address — every such
      attempt returns **404** (no existence leak); an unauthenticated `/me*` call → `401`.
- [ ] `identity.Reader` is satisfied: `*service.Service.UserByID` returns an `identity.PublicUser`
      (no password hash) and maps a missing user to `ErrUserNotFound`.
- [ ] `task test` (unit) green — `EnsureOwner`, `UserID`, `wantDefault`; P3 tests still pass.
- [ ] `task test:integration` green — address CRUD, default invariant, cross-user 404, profile update.
- [ ] `task ci` green (tidy → sqlc generate → lint → test → build).
- [ ] Repo matches the §4 delta; `domain`/`service` import no driver/SDK internals; the new table holds
      the `identity_` prefix; no cross-module FK; no new env vars introduced.

*Demo: log in as a buyer, add two addresses (watch the first become default and the second take the
default over), then try to fetch a second user's address by id and get a 404.*

---

## 11. Verification (PowerShell)

```powershell
# 1. Migrate + generate
task db:up
task migrate:up
task migrate:version          # -> 4
task sqlc

# 2. Build pipeline
task ci                       # tidy, sqlc generate, lint, test, build -> green

# 3. Run, then get a token (second terminal, after `task run`)
$body  = @{ email = "buyer@example.com"; password = "password123"; name = "Buyer One" } | ConvertTo-Json
$reg   = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/register -ContentType application/json -Body $body
$h     = @{ Authorization = "Bearer $($reg.tokens.access_token)" }

# 4. Profile read + update
Invoke-RestMethod -Method Get   -Uri http://localhost:8080/api/v1/me -Headers $h
Invoke-RestMethod -Method Patch -Uri http://localhost:8080/api/v1/me -Headers $h -ContentType application/json `
  -Body (@{ name = "Renamed Buyer" } | ConvertTo-Json)

# 5. Address book — first is default, second takes the default
$a1 = @{ recipient = "Buyer One"; line1 = "1 Main St"; city = "Cairo"; postal_code = "11511"; country = "EG" } | ConvertTo-Json
$r1 = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/me/addresses -Headers $h -ContentType application/json -Body $a1
$r1.is_default                # -> True (first address)
$a2 = @{ recipient = "Buyer One"; line1 = "2 Nile Ave"; city = "Giza"; postal_code = "12511"; country = "EG"; is_default = $true } | ConvertTo-Json
$r2 = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/me/addresses -Headers $h -ContentType application/json -Body $a2
Invoke-RestMethod -Method Get -Uri http://localhost:8080/api/v1/me/addresses -Headers $h   # a2 default, a1 not

# 6. Ownership rejection — a second user cannot see the first user's address (-> 404)
$reg2 = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/register -ContentType application/json `
  -Body (@{ email = "other@example.com"; password = "password123"; name = "Other" } | ConvertTo-Json)
$h2   = @{ Authorization = "Bearer $($reg2.tokens.access_token)" }
try {
  Invoke-RestMethod -Method Get -Uri "http://localhost:8080/api/v1/me/addresses/$($r1.id)" -Headers $h2
} catch { $_.Exception.Response.StatusCode.value__ }   # -> 404

# 7. The invariant + isolation guarantees, end to end
task test:integration         # default invariant + cross-user 404

# 8. The table exists with the identity_ prefix
docker compose exec postgres psql -U eco -d eco -c "\d identity_addresses"
```

---

## 12. Handoff to P5 (Seller Onboarding & Store)

P5 builds on the seams P4 created — no rework:
- **`identity.Reader` is live:** P5 receives `identitySvc` as `identity.Reader` (wire it in
  `cmd/api/main.go`) to resolve a user by id without importing `identity/service`/`repo`/`domain`.
- **The ownership contract is established:** P5's seller store/products use the **shared-resource**
  flavor — fetch then `auth.EnsureOwner(callerID, resource.OwnerID)` → **403** (PRD FR-9), distinct from
  P4's tenant-scoped **404** for private collections. The helper and its test already ship.
- **The `role` column is the role source of truth:** P5's `buyer → seller` transition updates
  `identity_users.role` (established in P3, read by the JWT issued at login) and gates seller routes via
  `auth.RequireRole("seller")`.
- **First real producer/consumer of business events returns:** P5 publishes `SellerApproved` /
  `SellerSuspended` through the **same outbox + `db.RunInTx`** producer pattern P3 used for
  `UserRegistered` (P4 published none — it reuses `RunInTx` only for atomic multi-row writes).
- **Admin-gated lifecycle pattern:** P5 introduces "admin = RBAC-gated operations on the owning module"
  (approve/reject/suspend), building on the `RequireRole("admin")` guard P3 demoed.
```
