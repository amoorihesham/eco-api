# Execution Plan — P3: Identity & Auth

| | |
|---|---|
| **Phase** | P3 — Identity & Auth (see [../IMPLEMENTATION_PLAN.md](../IMPLEMENTATION_PLAN.md)) |
| **Status** | Ready to implement |
| **Date** | 2026-06-16 |
| **Outcome** | The first **business module** ships. A visitor registers as a buyer, logs in, and calls a protected endpoint with a JWT; a wrong-role call is rejected. Access is a short-lived JWT (`userId` + `role`); refresh is an opaque, server-side-revocable token. Registration publishes `UserRegistered` atomically through the P2 outbox. The `identity` module is the **canonical template** (`domain / service / repo / handler / port`) every later module copies. |
| **Module path** | `github.com/amoorihesham/eco-api` |

> This is an **execution document**: detailed enough to implement directly. Code blocks are working
> skeletons — type them in, adjust names to taste. Builds directly on **P2**
> ([P2-eventing-foundation.md](P2-eventing-foundation.md)). Companion docs: [PRD](../PRD.md) ·
> [ARCHITECTURE](../ARCHITECTURE.md) · [OpenAPI](../../api/openapi.yaml).

---

## 1. Overview

**Objective.** Build the first real module — accounts, authentication, and roles — and with it the
**canonical module shape** every later module inherits. P3 introduces `internal/modules/identity`
(the `domain / service / repo / handler / port` template), the reusable `internal/platform/auth`
adapter (JWT issue/verify, bcrypt hashing, RBAC middleware), the first business tables (`identity_*`),
and the first real **producer** (`UserRegistered` via the P2 transactional outbox).

**In scope**
- The **identity module** template: `domain` (entities, events, sentinel errors) · `service`
  (use cases; depends only on **ports**) · `repo` (sqlc adapter) · `handler` (net/http transport) ·
  `port.go` (public surface).
- **`internal/platform/auth`** ports + adapters: `Hasher` (**bcrypt**), `TokenIssuer` / `Verifier`
  (**JWT HS256**, claims `sub`+`role`+`exp`), `Authn` middleware + `RequireRole` RBAC guard.
- **Endpoints** (OpenAPI **Authentication** tag): `register`, `login`, `refresh`, `logout`,
  `password/forgot`, `password/reset`.
- **Tokens.** Access = short-lived JWT. Refresh = 32-byte opaque random, stored **hashed**
  (`identity_refresh_tokens`) and **rotated** on use, **revocable** on logout / password reset.
- **Producer.** `register` writes `identity_users` **and** the `UserRegistered` outbox row in **one**
  `db.RunInTx` (the P2 §6 producer pattern).
- Migration `000003_identity` (`identity_users`, `identity_refresh_tokens`,
  `identity_password_resets`) + a **third sqlc block** emitting `…/repo/identitydb`.
- Config: `AUTH_*` (JWT secret, access/refresh/reset TTLs, bcrypt cost).
- A tiny **admin-only probe** route to demo the RBAC rejection.

**Out of scope (later phases)**
- `/me` **profile** read/update and the **address book** → **P4** (the rest of the OpenAPI **Account** tag).
- The **seller** role transition (`buyer → seller`) → **P5**; admin role-gated *business* routes → **P5+**.
- The first **consumer** of `UserRegistered` (welcome email) → **P16** (stretch).
- **Rate-limiting** on auth endpoints, real email delivery for password reset, and the
  **import-boundary lint gate** (services never import `pgx`/infra SDKs) → **P18**. In P3 these are
  rules/conventions, enforced later.

---

## 2. Prerequisites (Windows / PowerShell)

P0–P2 tools (Go, Docker Desktop, go-task, golangci-lint, golang-migrate, sqlc) still apply. P3 adds
**no new CLI tools** — two Go dependencies:

| Dependency | Min version | Install (PowerShell) | Verify |
|---|---|---|---|
| golang-jwt/jwt/v5 | v5.2+ | `go get github.com/golang-jwt/jwt/v5` | listed in `go.mod` |
| golang.org/x/crypto | v0.31+ | `go get golang.org/x/crypto/bcrypt` | listed in `go.mod` |

> The compose Postgres from P0 must be running for migrations and the integration tests:
> `task db:up` then `task migrate:up` (applies the new `000003` migration). `AUTH_JWT_SECRET` must be
> set (≥ 32 bytes) or the process refuses to boot — add it to `.env` (§7).

---

## 3. Tech stack & versions

| Concern | Choice |
|---|---|
| Password hashing | **bcrypt** (`golang.org/x/crypto/bcrypt`) behind the `auth.Hasher` port — cost configurable |
| Access token | **JWT HS256** (`golang-jwt/jwt/v5`); claims `sub`=userId, `role`, `iat`, `exp` |
| Refresh token | opaque 32-byte random (base64url), stored as a **SHA-256 hash** (`identity_refresh_tokens`), rotated on use |
| IDs | **google/uuid** — external-safe identifiers (ARCHITECTURE §12) |
| Queries / codegen | **sqlc** — a third `sql:` block emitting into `internal/modules/identity/repo/identitydb` |
| Atomicity | the P1 `db.RunInTx`; the outbox insert joins the same `tx` (P2 §6) |
| Unit-test mock | **pgxmock v4** (already present) for tx paths; pure-Go fakes for service/middleware |

> Adds `github.com/golang-jwt/jwt/v5` and `golang.org/x/crypto` (runtime). The refresh token is
> high-entropy random, so a fast **SHA-256** at-rest hash is sufficient and correct — **bcrypt is
> reserved for low-entropy passwords**. sqlc `overrides` (`timestamptz → time.Time`, `uuid → uuid.UUID`)
> carry over from P2 so the generated `identitydb` structs are clean Go.

---

## 4. Target file tree (delta on P2)

```text
eco-api/
├── go.mod                                          # CHANGED: + golang-jwt/jwt/v5, + golang.org/x/crypto
├── go.sum                                          # CHANGED (go mod tidy)
├── sqlc.yaml                                       # CHANGED: + third sql block (identitydb) with overrides
├── Taskfile.yml                                    # CHANGED: sqlc:check also diffs the identity gen dir
├── .env.example                                    # CHANGED: + AUTH_* vars
├── cmd/api/main.go                                 # CHANGED: build auth adapters + identity module; mount routes
├── internal/platform/
│   ├── config/
│   │   ├── config.go                               # CHANGED: AUTH_* fields + validation
│   │   └── config_test.go                          # CHANGED: + AUTH_JWT_SECRET required / defaults
│   └── auth/                                        # NEW package (platform adapter + ports)
│       ├── hasher.go                               # Hasher port + BcryptHasher
│       ├── token.go                                # TokenIssuer/Verifier ports + JWT (HS256), Claims, ErrInvalidToken
│       ├── middleware.go                           # Authn (verify bearer → ctx) + RequireRole RBAC guard
│       ├── context.go                              # ClaimsFrom(ctx)
│       ├── token_test.go                           # issue/verify round-trip + tampered/expired (no DB)
│       └── middleware_test.go                      # RBAC allow/deny + missing/bad token (httptest)
├── internal/modules/                               # NEW: first business module lives here
│   └── identity/
│       ├── port.go                                 # PUBLIC surface: published event + Reader port (for P4+)
│       ├── domain/
│       │   ├── user.go                             # User, Role; RefreshToken, PasswordReset value objects
│       │   ├── events.go                           # EventUserRegistered + UserRegisteredPayload
│       │   └── errors.go                           # ErrEmailTaken, ErrInvalidCredentials, ErrInvalidToken
│       ├── service/
│       │   ├── ports.go                            # Repository + Outbox ports (declared by the service)
│       │   ├── service.go                          # use cases (Register/Login/Refresh/Logout/Forgot/Reset)
│       │   └── service_test.go                     # credential logic with fakes (no DB)
│       ├── repo/
│       │   ├── repo.go                             # adapter: sqlc → domain, satisfies service.Repository
│       │   ├── queries/identity.sql                # sqlc queries (users, refresh tokens, resets)
│       │   ├── identitydb/                         # GENERATED + committed (db.go, models.go, identity.sql.go)
│       │   └── repo_integration_test.go            # //go:build integration: register persists + outbox row
│       └── handler/
│           ├── handler.go                          # decode → service → encode (httpx envelope)
│           └── routes.go                           # Mount(mux, authn) under /api/v1/auth
└── migrations/
    ├── 000003_identity.up.sql                      # identity_users + identity_refresh_tokens + identity_password_resets
    └── 000003_identity.down.sql
```

**Import-direction rule (carried from P1/P2, now first applied to a module):** `identity/domain` and
`identity/service` are **pure business logic** — they import only the standard library, `google/uuid`,
the module's own `domain`, and **ports** (`platform/auth` interfaces, `platform/db`, `platform/events`).
They must **never** import `pgx` driver internals, `bcrypt`, or `golang-jwt` directly; the composition
root injects the concrete adapters. `repo/` (and the generated `identitydb`) may import `pgx`; it is the
adapter. `platform/auth` may import `golang-jwt`/`bcrypt`. Convention now, a lint gate in **P18**.

> Pragmatic exception (precedent from P1 §6): the service orchestrates atomic writes with
> `db.RunInTx(ctx, pool, func(tx pgx.Tx) error { ... })`, so `service` references `pgx.Tx` as the
> unit-of-work currency and `db.Beginner` as the pool port. This is the established producer pattern;
> the import-boundary lint in P18 allow-lists `pgx.Tx`/`db` for service packages.

---

## 5. Execution steps

Work top to bottom; each step ends in a check. Assumes P2 is in place (`config`, `db` with `RunInTx`,
`events` with bus/outbox/idempotency, `httpx`, `health`, `cmd/api/main.go`).

### S1 — Add the dependencies
```powershell
go get github.com/golang-jwt/jwt/v5
go get golang.org/x/crypto/bcrypt
```
**Check:** `go.mod` lists both; `go mod tidy` is clean.

### S2 — Config: `AUTH_*`
Edit [../../internal/platform/config/config.go](../../internal/platform/config/config.go). Add the five
auth fields, their `Load` lines, and validation (full additions in §8). Key additions:

```go
// struct fields
AuthJWTSecret  string
AuthAccessTTL  time.Duration
AuthRefreshTTL time.Duration
AuthResetTTL   time.Duration
AuthBcryptCost int

// Load()
AuthJWTSecret:  env("AUTH_JWT_SECRET", ""),
AuthAccessTTL:  envDur("AUTH_ACCESS_TTL", 15*time.Minute),
AuthRefreshTTL: envDur("AUTH_REFRESH_TTL", 720*time.Hour),
AuthResetTTL:   envDur("AUTH_RESET_TTL", time.Hour),
AuthBcryptCost: envInt("AUTH_BCRYPT_COST", 12),

// Validate()
if len(strings.TrimSpace(c.AuthJWTSecret)) < 32 {
    return fmt.Errorf("AUTH_JWT_SECRET is required and must be >= 32 bytes")
}
if c.AuthBcryptCost < 10 || c.AuthBcryptCost > 31 {
    return fmt.Errorf("AUTH_BCRYPT_COST must be 10..31, got %d", c.AuthBcryptCost)
}
```
**Check:** `go build ./internal/platform/config`; empty `AUTH_JWT_SECRET` → load error.

### S3 — Identity migration
```powershell
task migrate:new -- identity   # creates migrations/000003_identity.{up,down}.sql
```
Fill the up/down files (full contents in §8): `identity_users`, `identity_refresh_tokens`,
`identity_password_resets` (all `identity_` prefixed; in-module FKs to `identity_users` are allowed —
P1 §6). Then:

```powershell
task db:up
task migrate:up
task migrate:version          # -> 3
```
**Check:** `migrate:version` prints `3` (not dirty); the three `identity_*` tables exist.

### S4 — sqlc: third block + queries
Add the identity `sql:` block to `sqlc.yaml` (reuse the P2 overrides) and create
`internal/modules/identity/repo/queries/identity.sql` (full in §8). Then:

```powershell
task sqlc                      # generates internal/modules/identity/repo/identitydb/*
```
The generated `identitydb` package is **committed**; `task sqlc:check` now guards all three gen dirs.
**Check:** `go build ./internal/modules/identity/repo/identitydb`.

### S5 — Auth platform (ports + adapters)
Create `internal/platform/auth/{hasher.go, token.go, middleware.go, context.go}` (full in §8): the
`Hasher` / `TokenIssuer` / `Verifier` ports, the `BcryptHasher` and `JWT` adapters, and the `Authn` +
`RequireRole` middleware producing the standard `httpx` error envelope.
**Check:** `go build ./internal/platform/auth`.

### S6 — Domain
Create `internal/modules/identity/domain/{user.go, events.go, errors.go}` (full in §8): the `User`
aggregate + `Role`, the `RefreshToken` / `PasswordReset` value objects, the `UserRegistered` event +
payload, and sentinel errors. Pure Go — no infra imports.
**Check:** `go build ./internal/modules/identity/domain`.

### S7 — Service (use cases)
Create `internal/modules/identity/service/{ports.go, service.go}` (full in §8). The service declares the
`Repository` and `Outbox` ports it needs and implements the six use cases. `Register` writes the user
**and** the `UserRegistered` outbox row in one `db.RunInTx` (atomic publish, P2 §6).
**Check:** `go build ./internal/modules/identity/service`.

### S8 — Repo (adapter)
Create `internal/modules/identity/repo/repo.go` (full in §8): wraps `identitydb` and maps rows ↔ domain;
write methods take `pgx.Tx` so the service can compose them with the outbox in one transaction.
**Check:** `go build ./internal/modules/identity/repo`.

### S9 — Handler + routes
Create `internal/modules/identity/handler/{handler.go, routes.go}` and `port.go` (full in §8): decode →
validate → service → encode via `httpx`; `Mount(mux, authn)` wires the six routes under `/api/v1/auth`,
with `logout` behind the `Authn` middleware.
**Check:** `go build ./internal/modules/identity/...`.

### S10 — Wire the module in `main`
Edit `cmd/api/main.go` (full file in §8): build the bcrypt hasher and JWT adapter, construct the repo →
service → handler, `events.NewOutbox(pool)`, mount the routes, and add the admin-only probe to demo
RBAC. (`bus.Subscribe("UserRegistered", …)` is reserved for **P16**.)
**Check:** `task run` boots; `POST /api/v1/auth/register` returns `201` with tokens.

### S11 — Env, Taskfile, tests
Add `AUTH_*` to `.env.example`; extend `sqlc:check` to diff the identity gen dir; write the tests (§9).
```powershell
task test                # unit (jwt, bcrypt, RBAC, credential logic) — Docker-free
task test:integration    # register persists + atomic outbox row; refresh/logout/reset lifecycle
task ci                  # tidy → sqlc generate → lint → test → build -> green
```
**Check:** `task ci` green; both suites pass.

---

## 6. The auth & module-template contract

This is the discipline every later module inherits — the P3 analog of P0's response/error envelope,
P1's table-ownership rule, and P2's outbox contract. It realizes ARCHITECTURE §5 (module structure),
§6 (ports & adapters), §7 (inter-module communication), and §10 (security).

**The canonical module shape** (`internal/modules/<name>/`)
- `domain/` — entities, value objects, domain events, invariants. **Pure Go**; no infra imports.
- `service/` — use cases. Declares the **ports** it needs (`Repository`, `Outbox`, and platform
  `auth` interfaces). Holds no transport or SQL detail.
- `repo/` — the adapter: sqlc-generated queries mapped to/from `domain`, satisfying `service.Repository`.
- `handler/` — net/http transport only: decode → call service → encode the `httpx` envelope.
- `port.go` — the module's **single public surface**: the events it publishes + the read interface
  other modules may consume. Siblings import **only** this file, never `service`/`repo`/`domain`.

**Auth rules**
- **Ports, not SDKs.** The service depends on `auth.Hasher`, `auth.TokenIssuer`, `auth.Verifier`
  (interfaces). The composition root injects `BcryptHasher` and `JWT`. The service never imports
  `bcrypt` or `golang-jwt`.
- **Access vs refresh.** Access = short-lived JWT carrying `sub`(userId) + `role`; the API never
  trusts a role from the request body, only from a verified token. Refresh = opaque random, stored
  **hashed**, **rotated** on every use (old row deleted), and **revoked** on logout / password reset.
- **RBAC at the edge.** `auth.Authn` verifies the bearer and puts `Claims` in context;
  `auth.RequireRole("admin")` returns `403 forbidden` otherwise. **Ownership/tenant isolation** (a user
  touches only its own resources) is a *service-level* rule introduced in **P4**.
- **Atomic publish (producer pattern).** State change + event on the **same `tx`**:

```go
err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
    if err := s.repo.CreateUser(ctx, tx, u); err != nil {       // module state
        return err
    }
    evt, err := events.NewEvent(domain.EventUserRegistered,
        domain.UserRegisteredPayload{UserID: u.ID, Email: u.Email})
    if err != nil {
        return err
    }
    return s.outbox.Write(ctx, tx, evt)                          // same tx → atomic
})
```

**Security invariants (baked into the handlers/service)**
- Never log passwords, hashes, tokens, or the JWT secret.
- `login` returns a **generic** `401` for both unknown email and wrong password (no account enumeration).
- `password/forgot` **always** returns `202` whether or not the email exists.
- Passwords are validated (`min 8`) at the handler boundary, returning the `validation_error` envelope.

| Concern | Type / helper |
|---|---|
| Hash / verify a password | `auth.Hasher.Hash(pw)` / `auth.Hasher.Compare(hash, pw)` |
| Issue an access token | `auth.TokenIssuer.Issue(userID, role)` → `(jwt, expiresIn, err)` |
| Verify a bearer token | `auth.Verifier.Verify(jwt)` → `auth.Claims{UserID, Role}` |
| Protect a route | `auth.Authn(verifier)` then `auth.RequireRole("admin")` (httpx middleware) |
| Read claims in a handler | `auth.ClaimsFrom(r.Context())` → `(Claims, bool)` |
| Publish atomically | `outbox.Write(ctx, tx, evt)` inside `db.RunInTx` |

---

## 7. Configuration reference (additions to P2)

| Env var | Type | Default | Required from |
|---|---|---|---|
| `AUTH_JWT_SECRET` | string (≥32 bytes) | — | **P3** (required) |
| `AUTH_ACCESS_TTL` | duration | `15m` | P3 |
| `AUTH_REFRESH_TTL` | duration | `720h` (30d) | P3 |
| `AUTH_RESET_TTL` | duration | `1h` | P3 |
| `AUTH_BCRYPT_COST` | int (10..31) | `12` | P3 |

All P0–P2 variables (`HTTP_*`, `LOG_*`, `ENVIRONMENT`, `DATABASE_URL`, `DB_*`, `OUTBOX_*`) are unchanged.

---

## 8. Full file contents

**`migrations/000003_identity.up.sql`**
```sql
-- P3 Identity & Auth: the first business module's tables (identity_ prefix; P1 ownership rule).
-- In-module FKs to identity_users are allowed; no cross-module FKs.

CREATE TABLE identity_users (
    id            uuid        PRIMARY KEY,
    email         citext      NOT NULL UNIQUE,           -- case-insensitive (baseline citext extension)
    password_hash text        NOT NULL,                  -- bcrypt; never returned by the API
    name          text        NOT NULL,
    role          text        NOT NULL DEFAULT 'buyer',  -- buyer | seller | admin (PRD FR-4: exactly one)
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Opaque refresh tokens, stored hashed. Rotated on use; deleted on logout / password reset.
CREATE TABLE identity_refresh_tokens (
    id         uuid        PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES identity_users(id) ON DELETE CASCADE,
    token_hash text        NOT NULL UNIQUE,              -- sha256(opaque token)
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_identity_refresh_tokens_user ON identity_refresh_tokens (user_id);

-- Time-limited, single-use password-reset tokens (used_at IS NULL = still valid).
CREATE TABLE identity_password_resets (
    id         uuid        PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES identity_users(id) ON DELETE CASCADE,
    token_hash text        NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
```

**`migrations/000003_identity.down.sql`**
```sql
DROP TABLE IF EXISTS identity_password_resets;
DROP TABLE IF EXISTS identity_refresh_tokens;
DROP TABLE IF EXISTS identity_users;
```

**`sqlc.yaml`** (add the third `sql:` block; keep the P1/P2 blocks unchanged)
```yaml
  - engine: postgresql
    schema: "migrations/*.up.sql"
    queries: "internal/modules/identity/repo/queries"
    gen:
      go:
        package: "identitydb"
        out: "internal/modules/identity/repo/identitydb"
        sql_package: "pgx/v5"
        emit_interface: true
        emit_empty_slices: true
        overrides:
          - db_type: "timestamptz"
            go_type: "time.Time"
          - db_type: "uuid"
            go_type: "github.com/google/uuid.UUID"
```

**`internal/modules/identity/repo/queries/identity.sql`**
```sql
-- name: CreateUser :exec
INSERT INTO identity_users (id, email, password_hash, name, role, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetUserByEmail :one
SELECT id, email, password_hash, name, role, created_at
FROM identity_users WHERE email = $1;

-- name: GetUserByID :one
SELECT id, email, password_hash, name, role, created_at
FROM identity_users WHERE id = $1;

-- name: UpdatePasswordHash :exec
UPDATE identity_users SET password_hash = $2 WHERE id = $1;

-- name: InsertRefreshToken :exec
INSERT INTO identity_refresh_tokens (id, user_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4);

-- name: GetRefreshToken :one
SELECT id, user_id, token_hash, expires_at
FROM identity_refresh_tokens WHERE token_hash = $1;

-- name: DeleteRefreshToken :exec
DELETE FROM identity_refresh_tokens WHERE token_hash = $1;

-- name: DeleteUserRefreshTokens :exec
DELETE FROM identity_refresh_tokens WHERE user_id = $1;

-- name: InsertPasswordReset :exec
INSERT INTO identity_password_resets (id, user_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4);

-- name: GetActivePasswordReset :one
SELECT id, user_id, token_hash, expires_at
FROM identity_password_resets
WHERE token_hash = $1 AND used_at IS NULL;

-- name: MarkPasswordResetUsed :exec
UPDATE identity_password_resets SET used_at = now() WHERE id = $1;
```

**`internal/platform/auth/hasher.go`**
```go
package auth

import "golang.org/x/crypto/bcrypt"

// Hasher hashes and verifies passwords. The service depends on this port, not on bcrypt.
type Hasher interface {
	Hash(plaintext string) (string, error)
	Compare(hash, plaintext string) error
}

// BcryptHasher is the MVP adapter (ARCHITECTURE §10: bcrypt/argon2).
type BcryptHasher struct{ cost int }

func NewBcryptHasher(cost int) BcryptHasher {
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	return BcryptHasher{cost: cost}
}

func (h BcryptHasher) Hash(plaintext string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plaintext), h.cost)
	return string(b), err
}

func (h BcryptHasher) Compare(hash, plaintext string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
}
```

**`internal/platform/auth/token.go`**
```go
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// ErrInvalidToken is returned for any malformed, mis-signed, or expired access token.
var ErrInvalidToken = errors.New("invalid token")

// Claims is the verified identity carried by an access token.
type Claims struct {
	UserID uuid.UUID
	Role   string
}

// TokenIssuer mints access tokens; Verifier validates them. Two ports so handlers can depend narrowly.
type TokenIssuer interface {
	Issue(userID uuid.UUID, role string) (token string, expiresIn int, err error)
}
type Verifier interface {
	Verify(token string) (Claims, error)
}

// JWT is the HS256 adapter satisfying both ports.
type JWT struct {
	secret    []byte
	accessTTL time.Duration
}

func NewJWT(secret string, accessTTL time.Duration) JWT {
	return JWT{secret: []byte(secret), accessTTL: accessTTL}
}

func (j JWT) Issue(userID uuid.UUID, role string) (string, int, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":  userID.String(),
		"role": role,
		"iat":  now.Unix(),
		"exp":  now.Add(j.accessTTL).Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(j.secret)
	if err != nil {
		return "", 0, fmt.Errorf("sign token: %w", err)
	}
	return signed, int(j.accessTTL.Seconds()), nil
}

func (j JWT) Verify(token string) (Claims, error) {
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return j.secret, nil
	})
	if err != nil || !parsed.Valid {
		return Claims{}, ErrInvalidToken
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return Claims{}, ErrInvalidToken
	}
	sub, _ := mc["sub"].(string)
	uid, err := uuid.Parse(sub)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	role, _ := mc["role"].(string)
	return Claims{UserID: uid, Role: role}, nil
}
```

**`internal/platform/auth/context.go`**
```go
package auth

import "context"

type ctxKey int

const claimsKey ctxKey = iota

func withClaims(ctx context.Context, c Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

// ClaimsFrom returns the verified claims placed by Authn, if present.
func ClaimsFrom(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsKey).(Claims)
	return c, ok
}
```

**`internal/platform/auth/middleware.go`**
```go
package auth

import (
	"net/http"
	"strings"

	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// Authn verifies the bearer access token and stores the claims in the request context.
func Authn(v Verifier) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearer(r)
			if raw == "" {
				httpx.Unauthorized(w, "missing bearer token")
				return
			}
			claims, err := v.Verify(raw)
			if err != nil {
				httpx.Unauthorized(w, "invalid or expired token")
				return
			}
			next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
		})
	}
}

// RequireRole rejects a request whose verified role is not in the allow-list. Use after Authn.
func RequireRole(roles ...string) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := ClaimsFrom(r.Context())
			if !ok {
				httpx.Unauthorized(w, "authentication required")
				return
			}
			for _, want := range roles {
				if c.Role == want {
					next.ServeHTTP(w, r)
					return
				}
			}
			httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "insufficient role")
		})
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
```

**`internal/modules/identity/domain/user.go`**
```go
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Role is the single role each account carries (PRD FR-4).
type Role string

const (
	RoleBuyer  Role = "buyer"
	RoleSeller Role = "seller"
	RoleAdmin  Role = "admin"
)

// User is the identity aggregate. PasswordHash never leaves the module boundary.
type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	Name         string
	Role         Role
	CreatedAt    time.Time
}

// RefreshToken / PasswordReset are persisted value objects (the plaintext token is never stored).
type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
}

type PasswordReset struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
}
```

**`internal/modules/identity/domain/events.go`**
```go
package domain

import "github.com/google/uuid"

// EventUserRegistered is published (atomically, via the outbox) when a buyer registers.
// Consumed by notification (P16) for the welcome email.
const EventUserRegistered = "UserRegistered"

type UserRegisteredPayload struct {
	UserID uuid.UUID `json:"user_id"`
	Email  string    `json:"email"`
}
```

**`internal/modules/identity/domain/errors.go`**
```go
package domain

import "errors"

var (
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid or expired token")
)
```

**`internal/modules/identity/service/ports.go`**
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
// Write methods take pgx.Tx so the service composes them with the outbox in one RunInTx.
// Read methods return pgx.ErrNoRows when the row is absent (the service maps that to domain errors).
type Repository interface {
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
}

// Outbox is the publish port (satisfied by *events.Outbox) — kept narrow for testability.
type Outbox interface {
	Write(ctx context.Context, tx pgx.Tx, e events.Event) error
}
```

**`internal/modules/identity/service/service.go`**
```go
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

// Config holds the token lifetimes the service needs.
type Config struct {
	RefreshTTL time.Duration
	ResetTTL   time.Duration
}

// Service implements the identity use cases. It depends only on ports.
type Service struct {
	pool   db.Beginner
	repo   Repository
	hasher auth.Hasher
	issuer auth.TokenIssuer
	outbox Outbox
	cfg    Config
}

func New(pool db.Beginner, repo Repository, hasher auth.Hasher, issuer auth.TokenIssuer, outbox Outbox, cfg Config) *Service {
	return &Service{pool: pool, repo: repo, hasher: hasher, issuer: issuer, outbox: outbox, cfg: cfg}
}

// AuthResult is what register/login/refresh hand back to the handler.
type AuthResult struct {
	User         domain.User
	AccessToken  string
	RefreshToken string // plaintext — returned to the client once, never stored
	ExpiresIn    int
}

func (s *Service) Register(ctx context.Context, email, password, name string) (AuthResult, error) {
	if _, err := s.repo.GetUserByEmail(ctx, email); err == nil {
		return AuthResult{}, domain.ErrEmailTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return AuthResult{}, err
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return AuthResult{}, err
	}
	u := domain.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: hash,
		Name:         name,
		Role:         domain.RoleBuyer,
		CreatedAt:    time.Now().UTC(),
	}

	var result AuthResult
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.CreateUser(ctx, tx, u); err != nil {
			return err
		}
		evt, err := events.NewEvent(domain.EventUserRegistered,
			domain.UserRegisteredPayload{UserID: u.ID, Email: u.Email})
		if err != nil {
			return err
		}
		if err := s.outbox.Write(ctx, tx, evt); err != nil { // atomic publish (P2 §6)
			return err
		}
		result, err = s.issueTokens(ctx, tx, u)
		return err
	})
	if err != nil {
		return AuthResult{}, err
	}
	return result, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (AuthResult, error) {
	u, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthResult{}, domain.ErrInvalidCredentials // generic — no enumeration
		}
		return AuthResult{}, err
	}
	if err := s.hasher.Compare(u.PasswordHash, password); err != nil {
		return AuthResult{}, domain.ErrInvalidCredentials
	}
	var result AuthResult
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		result, err = s.issueTokens(ctx, tx, u)
		return err
	})
	if err != nil {
		return AuthResult{}, err
	}
	return result, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (AuthResult, error) {
	rt, err := s.repo.GetRefreshToken(ctx, hashToken(refreshToken))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthResult{}, domain.ErrInvalidToken
		}
		return AuthResult{}, err
	}
	if time.Now().After(rt.ExpiresAt) {
		return AuthResult{}, domain.ErrInvalidToken
	}
	u, err := s.repo.GetUserByID(ctx, rt.UserID)
	if err != nil {
		return AuthResult{}, err
	}
	var result AuthResult
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.DeleteRefreshToken(ctx, tx, rt.TokenHash); err != nil { // rotate
			return err
		}
		result, err = s.issueTokens(ctx, tx, u)
		return err
	})
	if err != nil {
		return AuthResult{}, err
	}
	return result, nil
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	return db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.DeleteRefreshToken(ctx, tx, hashToken(refreshToken))
	})
}

// ForgotPassword returns a reset token if the user exists; "" otherwise. The handler ALWAYS replies 202.
func (s *Service) ForgotPassword(ctx context.Context, email string) (string, error) {
	u, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil // no account enumeration
		}
		return "", err
	}
	token, err := newOpaqueToken()
	if err != nil {
		return "", err
	}
	pr := domain.PasswordReset{
		ID:        uuid.New(),
		UserID:    u.ID,
		TokenHash: hashToken(token),
		ExpiresAt: time.Now().UTC().Add(s.cfg.ResetTTL),
	}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.InsertPasswordReset(ctx, tx, pr)
	}); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	pr, err := s.repo.GetActivePasswordReset(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrInvalidToken
		}
		return err
	}
	if time.Now().After(pr.ExpiresAt) {
		return domain.ErrInvalidToken
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	return db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.UpdatePasswordHash(ctx, tx, pr.UserID, hash); err != nil {
			return err
		}
		if err := s.repo.MarkPasswordResetUsed(ctx, tx, pr.ID); err != nil {
			return err
		}
		return s.repo.DeleteUserRefreshTokens(ctx, tx, pr.UserID) // force re-login everywhere
	})
}

// issueTokens mints an access JWT and persists a fresh (rotated) refresh token on tx.
func (s *Service) issueTokens(ctx context.Context, tx pgx.Tx, u domain.User) (AuthResult, error) {
	access, expiresIn, err := s.issuer.Issue(u.ID, string(u.Role))
	if err != nil {
		return AuthResult{}, err
	}
	refresh, err := newOpaqueToken()
	if err != nil {
		return AuthResult{}, err
	}
	rt := domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    u.ID,
		TokenHash: hashToken(refresh),
		ExpiresAt: time.Now().UTC().Add(s.cfg.RefreshTTL),
	}
	if err := s.repo.InsertRefreshToken(ctx, tx, rt); err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: u, AccessToken: access, RefreshToken: refresh, ExpiresIn: expiresIn}, nil
}

func newOpaqueToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken stores/looks-up tokens by a fast SHA-256 (the token is already high-entropy random).
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
```

**`internal/modules/identity/repo/repo.go`** (sqlc field/struct names follow `task sqlc` output)
```go
package repo

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/modules/identity/repo/identitydb"
)

// Repo implements service.Repository over sqlc-generated queries.
type Repo struct{ q *identitydb.Queries }

func New(pool *pgxpool.Pool) *Repo { return &Repo{q: identitydb.New(pool)} }

func (r *Repo) CreateUser(ctx context.Context, tx pgx.Tx, u domain.User) error {
	return r.q.WithTx(tx).CreateUser(ctx, identitydb.CreateUserParams{
		ID:           u.ID,
		Email:        u.Email,
		PasswordHash: u.PasswordHash,
		Name:         u.Name,
		Role:         string(u.Role),
		CreatedAt:    u.CreatedAt,
	})
}

func (r *Repo) GetUserByEmail(ctx context.Context, email string) (domain.User, error) {
	row, err := r.q.GetUserByEmail(ctx, email)
	if err != nil {
		return domain.User{}, err
	}
	return toUser(row.ID, row.Email, row.PasswordHash, row.Name, row.Role, row.CreatedAt), nil
}

func (r *Repo) GetUserByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	row, err := r.q.GetUserByID(ctx, id)
	if err != nil {
		return domain.User{}, err
	}
	return toUser(row.ID, row.Email, row.PasswordHash, row.Name, row.Role, row.CreatedAt), nil
}

func (r *Repo) UpdatePasswordHash(ctx context.Context, tx pgx.Tx, userID uuid.UUID, hash string) error {
	return r.q.WithTx(tx).UpdatePasswordHash(ctx, identitydb.UpdatePasswordHashParams{ID: userID, PasswordHash: hash})
}

func (r *Repo) InsertRefreshToken(ctx context.Context, tx pgx.Tx, rt domain.RefreshToken) error {
	return r.q.WithTx(tx).InsertRefreshToken(ctx, identitydb.InsertRefreshTokenParams{
		ID: rt.ID, UserID: rt.UserID, TokenHash: rt.TokenHash, ExpiresAt: rt.ExpiresAt,
	})
}

func (r *Repo) GetRefreshToken(ctx context.Context, tokenHash string) (domain.RefreshToken, error) {
	row, err := r.q.GetRefreshToken(ctx, tokenHash)
	if err != nil {
		return domain.RefreshToken{}, err
	}
	return domain.RefreshToken{ID: row.ID, UserID: row.UserID, TokenHash: row.TokenHash, ExpiresAt: row.ExpiresAt}, nil
}

func (r *Repo) DeleteRefreshToken(ctx context.Context, tx pgx.Tx, tokenHash string) error {
	return r.q.WithTx(tx).DeleteRefreshToken(ctx, tokenHash)
}

func (r *Repo) DeleteUserRefreshTokens(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	return r.q.WithTx(tx).DeleteUserRefreshTokens(ctx, userID)
}

func (r *Repo) InsertPasswordReset(ctx context.Context, tx pgx.Tx, pr domain.PasswordReset) error {
	return r.q.WithTx(tx).InsertPasswordReset(ctx, identitydb.InsertPasswordResetParams{
		ID: pr.ID, UserID: pr.UserID, TokenHash: pr.TokenHash, ExpiresAt: pr.ExpiresAt,
	})
}

func (r *Repo) GetActivePasswordReset(ctx context.Context, tokenHash string) (domain.PasswordReset, error) {
	row, err := r.q.GetActivePasswordReset(ctx, tokenHash)
	if err != nil {
		return domain.PasswordReset{}, err
	}
	return domain.PasswordReset{ID: row.ID, UserID: row.UserID, TokenHash: row.TokenHash, ExpiresAt: row.ExpiresAt}, nil
}

func (r *Repo) MarkPasswordResetUsed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	return r.q.WithTx(tx).MarkPasswordResetUsed(ctx, id)
}

func toUser(id uuid.UUID, email, hash, name, role string, createdAt time.Time) domain.User {
	return domain.User{ID: id, Email: email, PasswordHash: hash, Name: name, Role: domain.Role(role), CreatedAt: createdAt}
}
```

> **sqlc output names:** `task sqlc` emits one model `IdentityUser`, per-query param structs
> (`CreateUserParams`, `InsertRefreshTokenParams`, `InsertPasswordResetParams`, `UpdatePasswordHashParams`),
> and — for the `:one` selects that read a column subset — row structs `GetRefreshTokenRow` /
> `GetActivePasswordResetRow` whose fields are exactly the listed columns. `GetUserByEmail` /
> `GetUserByID` select all six columns, so they return the full `IdentityUser` model (its fields are
> passed positionally into `toUser`).

**`internal/modules/identity/handler/handler.go`**
```go
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/modules/identity/service"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

type Handler struct{ svc *service.Service }

func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

// --- request/response DTOs (mirror the OpenAPI Authentication schemas) ---

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}
type forgotRequest struct {
	Email string `json:"email"`
}
type resetRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type userDTO struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}
type tokensDTO struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}
type authResponse struct {
	User   userDTO   `json:"user"`
	Tokens tokensDTO `json:"tokens"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decode(w, r, &req) {
		return
	}
	if errs := validateRegister(req); len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	res, err := h.svc.Register(r.Context(), strings.TrimSpace(req.Email), req.Password, strings.TrimSpace(req.Name))
	if err != nil {
		if errors.Is(err, domain.ErrEmailTaken) {
			httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict, "email already registered")
			return
		}
		httpx.Internal(w, "could not register")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toAuthResponse(res))
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Email) == "" || req.Password == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "email and password are required")
		return
	}
	res, err := h.svc.Login(r.Context(), strings.TrimSpace(req.Email), req.Password)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidCredentials) {
			httpx.Unauthorized(w, "invalid email or password")
			return
		}
		httpx.Internal(w, "could not log in")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAuthResponse(res))
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "refresh_token is required")
		return
	}
	res, err := h.svc.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidToken) {
			httpx.Unauthorized(w, "invalid or expired refresh token")
			return
		}
		httpx.Internal(w, "could not refresh")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, tokensDTO{
		AccessToken: res.AccessToken, RefreshToken: res.RefreshToken, TokenType: "bearer", ExpiresIn: res.ExpiresIn,
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.Logout(r.Context(), req.RefreshToken); err != nil {
		httpx.Internal(w, "could not log out")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Email) == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "email is required")
		return
	}
	if _, err := h.svc.ForgotPassword(r.Context(), strings.TrimSpace(req.Email)); err != nil {
		httpx.Internal(w, "could not process request")
		return
	}
	// P3: the reset token is issued + persisted; P16 emails it. NEVER log it in production.
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if !decode(w, r, &req) {
		return
	}
	if errs := validateReset(req); len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	if err := h.svc.ResetPassword(r.Context(), req.Token, req.NewPassword); err != nil {
		if errors.Is(err, domain.ErrInvalidToken) {
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "invalid or expired reset token")
			return
		}
		httpx.Internal(w, "could not reset password")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "invalid JSON body")
		return false
	}
	return true
}

func validateRegister(req registerRequest) []httpx.ErrorDetail {
	var errs []httpx.ErrorDetail
	if strings.TrimSpace(req.Email) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "email", Message: "email is required"})
	}
	if len(req.Password) < 8 {
		errs = append(errs, httpx.ErrorDetail{Field: "password", Message: "password must be at least 8 characters"})
	}
	if strings.TrimSpace(req.Name) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "name", Message: "name is required"})
	}
	return errs
}

func validateReset(req resetRequest) []httpx.ErrorDetail {
	var errs []httpx.ErrorDetail
	if strings.TrimSpace(req.Token) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "token", Message: "token is required"})
	}
	if len(req.NewPassword) < 8 {
		errs = append(errs, httpx.ErrorDetail{Field: "new_password", Message: "password must be at least 8 characters"})
	}
	return errs
}

func toAuthResponse(res service.AuthResult) authResponse {
	return authResponse{
		User: userDTO{
			ID:        res.User.ID.String(),
			Email:     res.User.Email,
			Name:      res.User.Name,
			Role:      string(res.User.Role),
			CreatedAt: res.User.CreatedAt.Format(time.RFC3339),
		},
		Tokens: tokensDTO{
			AccessToken: res.AccessToken, RefreshToken: res.RefreshToken, TokenType: "bearer", ExpiresIn: res.ExpiresIn,
		},
	}
}
```

**`internal/modules/identity/handler/routes.go`**
```go
package handler

import (
	"net/http"

	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// Mount registers the Authentication routes under /api/v1. logout requires a valid access token,
// so it is wrapped with the Authn middleware supplied by the composition root.
func (h *Handler) Mount(mux *http.ServeMux, authn httpx.Middleware) {
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.refresh)
	mux.Handle("POST /api/v1/auth/logout", authn(http.HandlerFunc(h.logout)))
	mux.HandleFunc("POST /api/v1/auth/password/forgot", h.forgotPassword)
	mux.HandleFunc("POST /api/v1/auth/password/reset", h.resetPassword)
}
```

**`internal/modules/identity/port.go`**
```go
package identity

import (
	"context"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
)

// EventUserRegistered is this module's published event (the single public surface for producers).
const EventUserRegistered = domain.EventUserRegistered

// Reader is the read port sibling modules (P4 account, P5 seller) consume to resolve a user by ID.
// They import ONLY this file — never service/repo/domain. *service.Service satisfies it (P4 wires it).
type Reader interface {
	UserByID(ctx context.Context, id uuid.UUID) (PublicUser, error)
}

// PublicUser is the cross-module projection (no password hash crosses the boundary).
type PublicUser struct {
	ID    uuid.UUID
	Email string
	Name  string
	Role  string
}
```

**`internal/platform/config/config.go`** (additions to the P2 version — add the five `AUTH_*` fields,
their `Load` lines, and the two `Validate` guards shown in S2; nothing else changes.)

**`cmd/api/main.go`** (updated — adds the identity module + auth wiring to the P2 version)
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

	identityhandler "github.com/amoorihesham/eco-api/internal/modules/identity/handler"
	identityrepo "github.com/amoorihesham/eco-api/internal/modules/identity/repo"
	identityservice "github.com/amoorihesham/eco-api/internal/modules/identity/service"
	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/config"
	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/events"
	"github.com/amoorihesham/eco-api/internal/platform/health"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
	applog "github.com/amoorihesham/eco-api/internal/platform/log"
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

	bus := events.NewBus(logger)
	dispatcher := events.NewDispatcher(pool, bus, logger, cfg.OutboxPollInterval, cfg.OutboxBatchSize)

	// --- identity module (P3): auth adapters → repo → service → handler ---
	hasher := auth.NewBcryptHasher(cfg.AuthBcryptCost)
	jwt := auth.NewJWT(cfg.AuthJWTSecret, cfg.AuthAccessTTL)
	outbox := events.NewOutbox(pool)
	identitySvc := identityservice.New(pool, identityrepo.New(pool), hasher, jwt, outbox,
		identityservice.Config{RefreshTTL: cfg.AuthRefreshTTL, ResetTTL: cfg.AuthResetTTL})
	identityH := identityhandler.New(identitySvc)
	// First consumer of UserRegistered (welcome email) is wired in P16:
	//   bus.Subscribe(identity.EventUserRegistered, events.Idempotent(pool, "notification", ...))

	router := newRouter(logger, healthH, identityH, jwt)

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
func newRouter(l *slog.Logger, h *health.Handler, identityH *identityhandler.Handler, verifier auth.Verifier) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.Live)
	mux.HandleFunc("GET /readyz", h.Ready)

	identityH.Mount(mux, auth.Authn(verifier))

	// Demo the RBAC guard (real role-gated routes arrive in P5+): verify bearer, then require admin.
	adminPing := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "admin ok"})
	})
	mux.Handle("GET /api/v1/admin/ping", auth.Authn(verifier)(auth.RequireRole("admin")(adminPing)))

	return httpx.Chain(mux, httpx.RequestID(), httpx.Logger(l), httpx.Recoverer(l))
}
```

**`Taskfile.yml`** — extend `sqlc:check` to guard all three generated dirs:
```yaml
  sqlc:check:
    desc: Fail if generated code is stale
    cmds:
      - sqlc generate
      - git diff --exit-code -- internal/platform/db/dbgen internal/platform/events/eventsdb internal/modules/identity/repo/identitydb
```

**`.env.example`** — append:
```text
# Auth (P3) — AUTH_JWT_SECRET is required and must be >= 32 bytes
AUTH_JWT_SECRET=dev-only-change-me-32-bytes-min-secret
AUTH_ACCESS_TTL=15m
AUTH_REFRESH_TTL=720h
AUTH_RESET_TTL=1h
AUTH_BCRYPT_COST=12
```

**`go.mod`** — after `go get` + `go mod tidy`:
```text
require (
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/pashagolub/pgxmock/v4 v4.9.0
	golang.org/x/crypto v0.31.0
)
```

---

## 9. Testing plan

| Test | File | Asserts | Needs DB? |
|---|---|---|---|
| JWT issue/verify | `auth/token_test.go` | round-trip yields the same userId+role; tampered/expired/wrong-alg → `ErrInvalidToken` | no |
| RBAC middleware | `auth/middleware_test.go` | no/invalid bearer → `401`; correct role → `200`; wrong role → `403` | no |
| Login credential logic | `identity/service/service_test.go` | unknown email & wrong password both → `ErrInvalidCredentials` (no enumeration) | no |
| Config: `AUTH_JWT_SECRET` | `config/config_test.go` | empty/short secret → error; TTL + bcrypt-cost defaults applied | no |
| Register persists + publishes | `identity/repo/repo_integration_test.go` | a user row **and** exactly one `UserRegistered` outbox row commit together; rollback leaves neither | yes |
| Auth lifecycle | `identity/repo/repo_integration_test.go` | login→refresh rotates (old refresh rejected)→logout revokes; duplicate email → `ErrEmailTaken`; reset deletes refresh tokens | yes |

**`internal/platform/auth/token_test.go`** (no Docker)
```go
package auth_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/platform/auth"
)

func TestJWTRoundTrip(t *testing.T) {
	j := auth.NewJWT("test-secret-at-least-32-bytes-long!!", 15*time.Minute)
	id := uuid.New()

	tok, expiresIn, err := j.Issue(id, "admin")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if expiresIn != 900 {
		t.Fatalf("want expiresIn 900, got %d", expiresIn)
	}
	claims, err := j.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.UserID != id || claims.Role != "admin" {
		t.Fatalf("claims mismatch: %+v", claims)
	}
}

func TestJWTRejectsTampered(t *testing.T) {
	j := auth.NewJWT("test-secret-at-least-32-bytes-long!!", time.Minute)
	tok, _, _ := j.Issue(uuid.New(), "buyer")
	if _, err := j.Verify(tok + "x"); err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestJWTRejectsExpired(t *testing.T) {
	j := auth.NewJWT("test-secret-at-least-32-bytes-long!!", -time.Minute) // already expired
	tok, _, _ := j.Issue(uuid.New(), "buyer")
	if _, err := j.Verify(tok); err == nil {
		t.Fatal("expected error for expired token")
	}
}
```

**`internal/platform/auth/middleware_test.go`** (no Docker)
```go
package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/platform/auth"
)

func TestRBAC(t *testing.T) {
	j := auth.NewJWT("test-secret-at-least-32-bytes-long!!", time.Hour)
	admin, _, _ := j.Issue(uuid.New(), "admin")
	buyer, _, _ := j.Issue(uuid.New(), "buyer")

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	protected := auth.Authn(j)(auth.RequireRole("admin")(ok))

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"bad token", "Bearer nope", http.StatusUnauthorized},
		{"wrong role", "Bearer " + buyer, http.StatusForbidden},
		{"right role", "Bearer " + admin, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ping", nil)
			if c.header != "" {
				r.Header.Set("Authorization", c.header)
			}
			w := httptest.NewRecorder()
			protected.ServeHTTP(w, r)
			if w.Code != c.want {
				t.Fatalf("want %d, got %d", c.want, w.Code)
			}
		})
	}
}
```

**`internal/modules/identity/service/service_test.go`** (no Docker — fakes; credential paths only)
```go
package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/modules/identity/service"
)

// fakeRepo implements service.Repository; only the read methods are exercised here.
type fakeRepo struct {
	user domain.User
	err  error
}

func (f fakeRepo) GetUserByEmail(_ context.Context, _ string) (domain.User, error) { return f.user, f.err }
func (f fakeRepo) GetUserByID(_ context.Context, _ uuid.UUID) (domain.User, error) { return f.user, f.err }
func (fakeRepo) CreateUser(context.Context, pgx.Tx, domain.User) error             { return nil }
func (fakeRepo) UpdatePasswordHash(context.Context, pgx.Tx, uuid.UUID, string) error { return nil }
func (fakeRepo) InsertRefreshToken(context.Context, pgx.Tx, domain.RefreshToken) error { return nil }
func (fakeRepo) GetRefreshToken(context.Context, string) (domain.RefreshToken, error) {
	return domain.RefreshToken{}, pgx.ErrNoRows
}
func (fakeRepo) DeleteRefreshToken(context.Context, pgx.Tx, string) error            { return nil }
func (fakeRepo) DeleteUserRefreshTokens(context.Context, pgx.Tx, uuid.UUID) error    { return nil }
func (fakeRepo) InsertPasswordReset(context.Context, pgx.Tx, domain.PasswordReset) error { return nil }
func (fakeRepo) GetActivePasswordReset(context.Context, string) (domain.PasswordReset, error) {
	return domain.PasswordReset{}, pgx.ErrNoRows
}
func (fakeRepo) MarkPasswordResetUsed(context.Context, pgx.Tx, uuid.UUID) error { return nil }

type fakeHasher struct{}

func (fakeHasher) Hash(p string) (string, error) { return "hash:" + p, nil }
func (fakeHasher) Compare(hash, p string) error {
	if hash == "hash:"+p {
		return nil
	}
	return errors.New("mismatch")
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	repo := fakeRepo{user: domain.User{PasswordHash: "hash:correct", Role: domain.RoleBuyer}}
	// pool/issuer/outbox are nil: Login returns before any transaction on a credential failure.
	svc := service.New(nil, repo, fakeHasher{}, nil, nil, service.Config{})

	if _, err := svc.Login(context.Background(), "a@b.com", "wrong"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials, got %v", err)
	}
}

func TestLoginRejectsUnknownEmail(t *testing.T) {
	repo := fakeRepo{err: pgx.ErrNoRows}
	svc := service.New(nil, repo, fakeHasher{}, nil, nil, service.Config{})

	if _, err := svc.Login(context.Background(), "missing@b.com", "whatever"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials (no enumeration), got %v", err)
	}
}
```

**`internal/modules/identity/repo/repo_integration_test.go`** (build-tagged; against compose Postgres)
```go
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
```

Run: `task test` (unit) and `task test:integration` (DB-backed).

---

## 10. Definition of Done

- [ ] `go get` of `golang-jwt/jwt/v5` + `golang.org/x/crypto` done; `go.mod` lists both; `go mod tidy` clean.
- [ ] `task migrate:up` applies cleanly; `task migrate:version` → `3` (not dirty); the three `identity_*` tables exist.
- [ ] `task sqlc` generates `internal/modules/identity/repo/identitydb/*`; `task sqlc:check` reports no diff for any of the three gen dirs.
- [ ] `AUTH_JWT_SECRET` is required: missing/short → process exits with a config error.
- [ ] `task run` boots; `POST /api/v1/auth/register` → `201` with `{user, tokens}`.
- [ ] `register → login → GET /api/v1/admin/ping` with the access token → `403` (buyer role); a token with `role=admin` → `200`; no/invalid bearer → `401`.
- [ ] `refresh` rotates (the old refresh token is then rejected); `logout` revokes; `password/forgot` → `202` for any email; `password/reset` updates the hash and revokes refresh tokens.
- [ ] `task test` (unit) green — JWT, RBAC, bcrypt, credential logic, config.
- [ ] `task test:integration` green — register persists a user **and** one `UserRegistered` outbox row atomically; duplicate email → conflict.
- [ ] `task ci` green (tidy → sqlc generate → lint → test → build).
- [ ] Repo matches the §4 tree; `domain`/`service` import no driver/SDK internals; all new tables hold the `identity_` prefix; no cross-module FK.

*Demo: a full auth round-trip through the API — register, log in, call a protected endpoint with the
token, and watch a wrong-role call get rejected.*

---

## 11. Verification (PowerShell)

```powershell
# 1. Migrate + generate
task db:up
task migrate:up
task migrate:version          # -> 3
task sqlc

# 2. Build pipeline
task ci                       # tidy, sqlc generate, lint, test, build -> green

# 3. Run, then exercise the auth round-trip (second terminal, after `task run`)
$body = @{ email = "buyer@example.com"; password = "password123"; name = "Buyer One" } | ConvertTo-Json
$reg  = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/register -ContentType application/json -Body $body
$access = $reg.tokens.access_token

# 4. Protected route + RBAC rejection (buyer hitting an admin-only route -> 403)
try {
  Invoke-RestMethod -Method Get -Uri http://localhost:8080/api/v1/admin/ping -Headers @{ Authorization = "Bearer $access" }
} catch { $_.Exception.Response.StatusCode.value__ }   # -> 403

# 5. Login + refresh + logout
$login = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/login -ContentType application/json `
  -Body (@{ email = "buyer@example.com"; password = "password123" } | ConvertTo-Json)
$ref = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/refresh -ContentType application/json `
  -Body (@{ refresh_token = $login.tokens.refresh_token } | ConvertTo-Json)
Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/logout -ContentType application/json `
  -Headers @{ Authorization = "Bearer $($ref.access_token)" } -Body (@{ refresh_token = $ref.refresh_token } | ConvertTo-Json)

# 6. The atomic-publish guarantee
task test:integration         # register persists user + exactly one UserRegistered outbox row

# 7. Tables exist with the identity_ prefix
docker compose exec postgres psql -U eco -d eco -c "\dt identity_*"
#   -> identity_users, identity_refresh_tokens, identity_password_resets
```

---

## 12. Handoff to P4 (Account: Profile & Addresses)

P4 builds on the seams P3 created — no rework:
- **Profile + addresses:** add `GET/PATCH /api/v1/me` and the address book (`/me/addresses`), the rest
  of the OpenAPI **Account** tag. Add `identity_addresses` (a fourth migration; `identity_` prefix).
- **Ownership / tenant isolation:** introduce the reusable rule "a user touches only its own resources,"
  read from `auth.ClaimsFrom(ctx)` — factor it as a shared helper (the negative cross-user case is the
  headline test).
- **Public read port:** wire `identity.Reader` (`port.go`) so sibling modules resolve a user by ID
  without importing `service`/`repo`.
- **Default-address invariant:** exactly one default per user (a P4 service rule).
- **Role transitions** (`buyer → seller`) and admin seller lifecycle arrive in **P5**, reading/writing
  the `role` column established here.
```
