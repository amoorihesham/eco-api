# Execution Plan — P6: Catalog: Categories, Products, Variants

| | |
|---|---|
| **Phase** | P6 — Catalog: Categories, Products, Variants (see [../IMPLEMENTATION_PLAN.md](../IMPLEMENTATION_PLAN.md)) |
| **Status** | Ready to implement |
| **Date** | 2026-06-16 |
| **Outcome** | The sellable **catalog** module ships (`internal/modules/catalog`). An admin manages categories (`POST /api/v1/categories`); an **approved** seller creates products **with and without** variants (`POST /api/v1/products`, `POST /api/v1/products/{id}/variants`) and flips them `draft → active`, which publishes `ProductPublished`. Catalog **consumes `SellerSuspended`** and deactivates that seller's active products, publishing `ProductUnpublished` for each — so a suspended seller's products stop being discoverable. Admin moderation (`POST /api/v1/products/{id}/unpublish`) and category retire (which **flags** referenced products for re-categorization instead of orphaning them) round out the surface. A `catalog.Reader` port exposes per-sellable-unit price/stock for P10 cart and P11 checkout. |
| **Module path** | `github.com/amoorihesham/eco-api` |

> This is an **execution document**: detailed enough to implement directly. Code blocks are working
> skeletons — type them in, adjust names to taste. Builds on **P5** ([P5-seller-onboarding-store.md](P5-seller-onboarding-store.md))
> and copies the P3 module template ([P3-identity-auth.md](P3-identity-auth.md)) wholesale.
> Companion docs: [PRD](../PRD.md) · [ARCHITECTURE](../ARCHITECTURE.md) · [OpenAPI](../../api/openapi.yaml).

---

## 1. Overview

**Objective.** Build the sellable **catalog** — admin-managed categories, seller-owned products with a
`draft / active / inactive` lifecycle, and the **optional two-axis (color + size) variant model**. With it,
establish the **sellable-unit** concept (a variant, or the variant-less product) and the **catalog query
port** that cart/checkout consume by id. P6 adds the new `internal/modules/catalog` module (canonical
`domain / service / repo / handler / port` shape), a sixth migration (`catalog_categories`,
`catalog_products`, `catalog_variants`), and a **fifth sqlc target** (`catalogdb`). It is the project's
**second cross-module event consumer** (`SellerSuspended` → hide products), mirroring P5's identity consumer.

**In scope**
- **Categories** (admin-managed; optional `parent_id`) — public `GET /api/v1/categories`; admin
  `POST /api/v1/categories`, `PATCH /api/v1/categories/{categoryId}`, `DELETE /api/v1/categories/{categoryId}`.
  Retire is a **soft retire** (`retired_at`): it **must not orphan products** — referenced products are
  **flagged** `needs_recategorization` (PRD FR-41). Schemas `Category` / `CategoryInput`.
- **Products** (seller-owned CRUD; status `draft / active / inactive`) — `POST /api/v1/products`,
  `PATCH /api/v1/products/{productId}`, `DELETE /api/v1/products/{productId}`. A product belongs to exactly
  one seller and one category (FR-10). Money is **integer minor units** (FR/ARCHITECTURE §8). Schemas
  `Product` / `ProductInput`.
- **Variants** (optional; ≤2 axes — color, size; per-variant SKU/price/stock, FR-12–FR-14) —
  `POST /api/v1/products/{productId}/variants`, `PATCH .../variants/{variantId}`,
  `DELETE .../variants/{variantId}`. Schemas `Variant` / `VariantInput`.
- **Owner read-back** — `GET /api/v1/products/{productId}` (owner or admin; includes drafts) and
  `GET /api/v1/seller/products` (the caller's own products). Verifies the DoD without pulling P9 forward.
- **Admin moderation** — `POST /api/v1/products/{productId}/unpublish` (immediate takedown), behind
  `auth.RequireRole("admin")`.
- **Events published** — `ProductPublished` / `ProductUnpublished` (Appendix A; consumed by P10 cart),
  written to the outbox **atomically** with the status change (the P2/P3/P5 producer pattern).
- **Second cross-module consumer** — `catalog` **subscribes** to `SellerSuspended` and deactivates that
  seller's active products inside an idempotency transaction (`events.Idempotent`).
- **Catalog query port** — `catalog.Reader` (`GetSellable` by product/variant id → price, stock, currency,
  active flag) exposed in `port.go` for **P10 cart / P11 checkout** to consume synchronously.
- Migration `000006_catalog`, a new `catalog.sql`, and a new `catalogdb` sqlc target wired into `sqlc.yaml`
  + `SQLC_DIRS`.

**Out of scope (later phases)**
- Public **Discovery** — listing with filters/sort, keyword search, and the rich public product detail
  (with seller info + availability) → **P9** (consumes the `catalog.Reader` and the visibility rule
  established here). P6 ships only owner/admin reads.
- Product **images / media** upload + the storage port → **P7** (slots onto the product established here).
- **Stock decrement** semantics and the authoritative availability service → **P8 / P12**. P6 only stores
  stock numbers per sellable unit; it never decrements.
- **Seller reports** → P14; **fulfilment** → P13; **payments** → P12.
- **Rate-limiting** and the **import-boundary lint gate** → **P18** (conventions now, enforced later).

**Depends on.** P5 (the `seller.Reader` port + `SellerSuspended` event + the new-module/admin-on-owning-module
patterns), P2 (events: bus + outbox + `Idempotent`), P3 (`auth` RBAC, the module template, the outbox
producer pattern).

---

## 2. Prerequisites (Windows / PowerShell)

P6 adds **no new tools and no new Go dependencies** — everything (`pgx`, `sqlc`, `golang-migrate`,
`google/uuid`, the `events`/`auth`/`httpx`/`db` platform packages, the `seller` port) is present from P0–P5.

| Need | Why | Command |
|---|---|---|
| Compose Postgres running | migrations + integration tests | `task db:up` |
| P5 migration applied | the schema must be at version `5` before adding `000006` | `task migrate:up` → `task migrate:version` ⇒ `5` |
| `AUTH_JWT_SECRET` set (≥32 bytes) | the server + `Authn`/`RequireRole` middleware | already in `.env` from P3 |
| An **admin** account | to exercise category writes + product unpublish | register, then `UPDATE identity_users SET role='admin'` (see §11) |
| An **approved seller** account | to create products/variants | run the P5 apply→approve round-trip (see §11) |

> No `go get` in this phase. If `task migrate:version` is below `5`, finish P5 first.

---

## 3. Tech stack & versions

Unchanged from P3–P5 — P6 reuses the established stack. The **new patterns** (not new tech) are listed for clarity.

| Concern | Choice (carried from P3–P5 unless noted) |
|---|---|
| New module shape | the P3 canonical template copied wholesale: `domain / service / repo / handler / port.go` |
| Transport | stdlib `net/http` `ServeMux`; path params via `r.PathValue("productId")` / `"variantId"` / `"categoryId"` |
| RBAC | `auth.Authn` + `auth.RequireRole("admin")` (categories, unpublish) / `RequireRole("seller")` (product/variant writes) |
| Cross-module **read** (sync) | `seller.Reader.SellerStatus` (P5 port) — gate writes on `approved`; the catalog `Reader` it itself exposes |
| Cross-module **reaction** (async) | **new consumer** — `catalog` subscribes to `seller.EventSellerSuspended` via `bus.Subscribe` + `events.Idempotent(pool, "catalog", …)` |
| Producer atomicity | the P3/P5 pattern: state change + `outbox.Write(ctx, tx, evt)` in one `db.RunInTx` |
| Status state machine | `text` column + CHECK (`draft`/`active`/`inactive`); status→visibility decided in the service |
| Sellable unit | a variant, or the variant-less product (FR-12–FR-14); §6 contract |
| Money | `bigint` minor units (`base_price_minor`, `price_minor`) → Go `int64`; never floats |
| Nullable columns | the `catalogdb` block sets `emit_pointers_for_null_types: true` → `parent_id`/`color`/`size` become `*T` |
| Queries / codegen | **sqlc** — a **fifth** `sql:` block emitting `internal/modules/catalog/repo/catalogdb` |
| Unit-test mock | pure-Go fakes (`fakeRepo`, `fakeSeller`) for status-transition / ownership / gate paths; DB-backed for the rest |

---

## 4. Target file tree (delta on P5)

```text
eco-api/
├── sqlc.yaml                                          # CHANGED: + fifth sql block (catalogdb)
├── Taskfile.yml                                       # CHANGED: SQLC_DIRS += catalog gen dir
├── cmd/api/main.go                                    # CHANGED: build catalog module + subscribe catalog to SellerSuspended
├── migrations/
│   ├── 000006_catalog.up.sql                          # NEW: catalog_categories + catalog_products + catalog_variants
│   └── 000006_catalog.down.sql                        # NEW
└── internal/modules/
    └── catalog/                                       # NEW MODULE (copies the P3/P5 template)
        ├── port.go                                    # PUBLIC surface: events + payloads + Sellable + Reader port
        ├── domain/
        │   ├── catalog.go                             # Category, Product, Variant, Status, Sellable + guards/slugify
        │   ├── events.go                              # ProductPublished/ProductUnpublished consts + payloads
        │   └── errors.go                              # sentinels (ErrProductNotFound, ErrNotOwner, ErrSellerNotApproved, …)
        ├── service/
        │   ├── ports.go                               # Repository + Outbox ports (service declares what it needs)
        │   ├── service.go                             # category + product + variant use cases (consume seller.Reader)
        │   ├── visibility.go                          # status→event helper + HideSellerProducts (SellerSuspended consumer)
        │   ├── reader.go                              # GetSellable — satisfies catalog.Reader (P10/P11 consume)
        │   └── service_test.go                        # transition/ownership/gate guards with fakes (no DB)
        ├── repo/
        │   ├── repo.go                                # adapter: catalogdb → domain, satisfies service.Repository
        │   ├── queries/catalog.sql                    # sqlc queries
        │   ├── catalogdb/                             # GENERATED + committed (task sqlc)
        │   └── catalog_integration_test.go            # //go:build integration: create ±variants; suspend→hide; retire→flag
        └── handler/
            ├── handler.go                             # DTOs + decode→service→encode (httpx envelope)
            └── routes.go                              # Mount: /categories, /products, /seller/products with RBAC
```

**Import-direction rule (carried from P3–P5):** `catalog/domain` and `catalog/service` stay pure — stdlib,
`google/uuid`, the module's own `domain`, ports (`platform/db`, `platform/events`), and the **sibling port
package** `internal/modules/seller` (only `port.go`, never its `service`/`repo`/`domain`). `service` may name
`pgx.Tx`/`db.Beginner` as the unit-of-work currency (the P1 §6 allow-listed exception). Only `repo/` imports
the `pgx` driver. **The seller package gains no `catalog` import** — the `SellerSuspended` consumer closure
lives in `cmd/api/main.go` (the composition root, which alone knows both modules).

---

## 5. Execution steps

Work top to bottom; each step ends in a check. Assumes P5 is complete (schema at version `5`, the `seller`
module + its `Reader` port + `SellerSuspended` event, `auth`, `httpx`, `events`, `db.RunInTx`, compose Postgres).

### S1 — Migration: `catalog_categories` + `catalog_products` + `catalog_variants`
```powershell
task migrate:new -- catalog      # creates migrations/000006_catalog.{up,down}.sql
```
Fill the up/down files (full contents in §8): three `catalog_`-prefixed tables, the status `CHECK`, the
`slug` unique index, the per-`(product_id, sku)` unique index, helpful lookup indexes, and the
`needs_recategorization` / soft-retire columns. `seller_id` / `category_id` / `product_id` are **plain
UUIDs** — no FK across module prefixes (`catalog_variants.product_id` may FK within the module). Then:

```powershell
task migrate:up
task migrate:version            # -> 6
```
**Check:** `migrate:version` prints `6` (not dirty); the three tables exist with `uq_catalog_categories_slug`
and `uq_catalog_variants_product_sku`.

### S2 — sqlc: fifth target (`catalogdb`)
Append the `catalogdb` block to [../../sqlc.yaml](../../sqlc.yaml) (reuse the timestamptz/uuid overrides and
**add `emit_pointers_for_null_types: true`** for `parent_id`/`color`/`size`), create
`internal/modules/catalog/repo/queries/catalog.sql` (full in §8), and add the new gen dir to `SQLC_DIRS` in
[../../Taskfile.yml](../../Taskfile.yml). Then:

```powershell
task sqlc                        # generates internal/modules/catalog/repo/catalogdb/*
```
**Check:** `go build ./internal/modules/catalog/repo/catalogdb`; `task sqlc:check` clean after commit.

### S3 — Catalog domain
Create `internal/modules/catalog/domain/{catalog.go, events.go, errors.go}` (full in §8): the `Category`,
`Product`, `Variant`, `Sellable` value objects, the `Status` type + helpers (`Valid`, `IsDiscoverable`,
`Slugify`), the event consts + payloads, and the sentinel errors. Pure Go.
**Check:** `go build ./internal/modules/catalog/domain`.

### S4 — Catalog service + ports + port.go
Create `service/ports.go` (the `Repository` + `Outbox` ports), `service/service.go` (category/product/variant
use cases, consuming `seller.Reader`), `service/visibility.go` (the status→event helper + `HideSellerProducts`
consumer method), `service/reader.go` (`GetSellable`), and the module's `port.go` (events + payloads +
`Sellable` + `Reader`). Full in §8.
**Check:** `go build ./internal/modules/catalog/service ./internal/modules/catalog`.

### S5 — Catalog repo adapter
Create `repo/repo.go` mapping `catalogdb` rows ↔ domain (full in §8); writes take `pgx.Tx`, reads take `ctx`
and return `pgx.ErrNoRows` (the P3/P5 convention).
**Check:** `go build ./internal/modules/catalog/repo`.

### S6 — Catalog handler + routes
Create `handler/handler.go` (DTOs, validation, the handlers) and `handler/routes.go` (mount under `/api/v1`
with `Authn`, `RequireRole("seller")` for product/variant writes + the owner read, `RequireRole("admin")` for
category writes + unpublish; public `GET /categories`). Full in §8.
**Check:** `go build ./internal/modules/catalog/...`.

### S7 — Wire in `main`, then run + test
Edit `cmd/api/main.go` (full file in §8): build `catalog` repo → service → handler (passing `sellerSvc` as
`seller.Reader`), **subscribe catalog to `SellerSuspended`** before the dispatcher starts, and mount the routes.
```powershell
task run                         # boots; admin creates a category; approved seller creates a product
task test                        # unit: status→event, ownership, approved-gate, variant validation
task test:integration            # create ±variants + atomic publish; suspend→hide; retire→flag; dedupe
task ci                          # tidy → sqlc generate → lint → test → build -> green
```
**Check:** `task ci` green; both suites pass.

---

## 6. The sellable-unit & catalog-query-port contract

This is the discipline P6 establishes — the analog of P3's auth/module template, P4's ownership rule, P5's
admin-on-owning-module rule. It realizes ARCHITECTURE **Rule 3** (no cross-module FK/joins; references by
**id**, resolved via ports or events), **Rule 4** (*event-driven first*; synchronous calls reserved for
**reads** a request cannot proceed without), **§5.3** (*admin is not a module*), and **§8** (money in minor units).

**The sellable unit = a variant, or the variant-less product (FR-12–FR-14).**
- A product with **no variants** carries its own `base_price_minor` + `stock`; that product *is* the sellable
  unit.
- A product with **variants** (`has_variants = true`) is **not itself sold** — each variant owns its `sku`
  (unique per product), `price_minor`, and `stock`; the **product-level stock is unused** (stored `0`, never
  read for sale). Adding the first variant flips `has_variants → true`; deleting the last flips it back.
- Variant axes are limited to **color** and **size** (≤2). There is no third axis — it is structurally
  impossible, not validated away.

**Status → visibility (FR-11).** A product is discoverable only when `status = active` **and** its seller is
`approved`. The status edge drives the event, emitted **atomically** via the outbox inside the same `db.RunInTx`
as the write:

| Edge | Event |
|---|---|
| `draft`/`inactive` → `active` (create-as-active or activate) | `ProductPublished` |
| `active` → `inactive` (deactivate / admin unpublish / delete an active product) | `ProductUnpublished` |
| `SellerSuspended` → deactivate the seller's active products | `ProductUnpublished` (one per product) |
| any non-visibility edge (`draft` ↔ `inactive`, field edits) | *(none)* |

`ProductPublished` / `ProductUnpublished` are consumed by **P10 cart** (prune stale lines) and **P16**
notifications. Delivery is at-least-once; consumers dedupe by `event_id`.

**ID-only cross-module references.** `catalog_products.seller_id` and `.category_id` are **plain UUIDs** — no
FK, no join to another module. A seller's status is resolved at request time via `seller.Reader`, **never** by
reading seller tables. (`catalog_variants.product_id → catalog_products.id` is an in-module FK and allowed.)

**Admin = RBAC-gated operations on the owning module.** Category writes and product `unpublish` are
`RequireRole("admin")` handlers on the **catalog** module — there is no admin module (continues P5 §6).

**Seller write gate.** Product/variant writes sit behind `RequireRole("seller")` **and** a service check that
`seller.Reader.SellerStatus(caller) == approved` (a suspended seller keeps `role=seller` but cannot
create/edit → `ErrSellerNotApproved` → **403**), **and** per-resource ownership (`product.seller_id == caller`
→ else `ErrNotOwner`, **403**; FR-15). The caller id always comes from the verified token (`auth.UserID`).

**Category retire must not orphan products (FR-41).** `DELETE /categories/{id}` is a **soft retire**
(`retired_at = now()`, hidden from `GET /categories`); in the same tx, products in that category are **flagged**
`needs_recategorization = true` (their `category_id` is left intact, so nothing is orphaned). Re-categorization
UX is later; P6 only guarantees the flag.

**The catalog query port.** `catalog.Reader.GetSellable(ctx, productID, variantID)` returns the sellable unit's
`{SellerID, PriceMinor, Currency, Stock, Active}` — `variantID = uuid.Nil` selects the variant-less product.
**P10 cart** and **P11 checkout** consume it synchronously (a read the request cannot proceed without, Rule 4);
they never read `catalog_*` tables.

| Concern | Type / helper |
|---|---|
| Gate an admin action | `auth.Authn` + `auth.RequireRole("admin")` (httpx middleware) |
| Gate a seller write | `RequireRole("seller")` **and** `SellerStatus == approved` **and** ownership (→ 403 otherwise) |
| Publish atomically | `outbox.Write(ctx, tx, evt)` inside `db.RunInTx` (P3/P5 producer pattern) |
| React to a sibling's event | `bus.Subscribe(seller.EventSellerSuspended, events.Idempotent(pool, "catalog", txHandler))` (P2) |
| Resolve seller status (sync read) | `seller.Reader.SellerStatus(ctx, userID)` → `seller.Status` |
| Expose a sellable unit to siblings | `catalog.Reader.GetSellable(ctx, productID, variantID)` (P10/P11 consume) |

**Contracts & events (from IMPLEMENTATION_PLAN).** catalog query port (product/price lookups for cart &
order); publishes `ProductPublished`/`ProductUnpublished`; consumes `SellerSuspended`.

---

## 7. Configuration reference (additions to P5)

**None.** P6 introduces no new environment variables. The existing `OUTBOX_POLL_INTERVAL` / `OUTBOX_BATCH_SIZE`
(P2) govern how quickly the `SellerSuspended` consumer and the `ProductPublished`/`ProductUnpublished`
dispatch run; all `AUTH_*`, `DB_*`, `HTTP_*`, `LOG_*` settings are unchanged and sufficient.

---

## 8. Full file contents

**`migrations/000006_catalog.up.sql`**
```sql
-- P6 Catalog: the catalog module's tables (catalog_ prefix; P1 ownership rule).
-- seller_id / category_id are plain UUIDs referencing other concerns BY ID ONLY — no cross-module FK
-- (ARCHITECTURE Rule 3). catalog_variants.product_id is an IN-MODULE FK and is allowed.

CREATE TABLE catalog_categories (
    id         uuid        PRIMARY KEY,
    name       text        NOT NULL,
    slug       text        NOT NULL,
    parent_id  uuid,                                    -- nullable: top-level categories have none
    retired_at timestamptz,                             -- soft retire (FR-41); NULL = active
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_catalog_categories_slug ON catalog_categories (slug);

CREATE TABLE catalog_products (
    id                     uuid        PRIMARY KEY,
    seller_id              uuid        NOT NULL,         -- plain UUID (identity user id); no FK
    category_id            uuid        NOT NULL,         -- plain UUID (catalog_categories.id, resolved in-service)
    title                  text        NOT NULL,
    description            text        NOT NULL DEFAULT '',
    status                 text        NOT NULL DEFAULT 'draft'
                                       CHECK (status IN ('draft', 'active', 'inactive')),
    base_price_minor       bigint      NOT NULL DEFAULT 0 CHECK (base_price_minor >= 0),
    currency               text        NOT NULL,
    has_variants           boolean     NOT NULL DEFAULT false,
    stock                  integer     NOT NULL DEFAULT 0 CHECK (stock >= 0),  -- unused when has_variants
    needs_recategorization boolean     NOT NULL DEFAULT false,                 -- set when its category is retired
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_catalog_products_seller   ON catalog_products (seller_id);
CREATE INDEX idx_catalog_products_category ON catalog_products (category_id);
CREATE INDEX idx_catalog_products_status   ON catalog_products (status);

CREATE TABLE catalog_variants (
    id          uuid        PRIMARY KEY,
    product_id  uuid        NOT NULL REFERENCES catalog_products (id) ON DELETE CASCADE,  -- in-module FK
    color       text,                                   -- nullable axis
    size        text,                                   -- nullable axis
    sku         text        NOT NULL,
    price_minor bigint      NOT NULL DEFAULT 0 CHECK (price_minor >= 0),
    stock       integer     NOT NULL DEFAULT 0 CHECK (stock >= 0),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_catalog_variants_product_sku ON catalog_variants (product_id, sku);
CREATE INDEX idx_catalog_variants_product ON catalog_variants (product_id);
```

**`migrations/000006_catalog.down.sql`**
```sql
DROP TABLE IF EXISTS catalog_variants;
DROP TABLE IF EXISTS catalog_products;
DROP TABLE IF EXISTS catalog_categories;
```

**`sqlc.yaml`** (append the fifth `sql:` block; keep the P1/P2/P3/P5 blocks unchanged)
```yaml
  - engine: postgresql
    schema: "migrations/*.up.sql"
    queries: "internal/modules/catalog/repo/queries"
    gen:
      go:
        package: "catalogdb"
        out: "internal/modules/catalog/repo/catalogdb"
        sql_package: "pgx/v5"
        emit_interface: true
        emit_empty_slices: true
        emit_pointers_for_null_types: true   # parent_id/color/size -> *T (avoids pgtype scanning)
        overrides:
          - db_type: "timestamptz"
            go_type: "time.Time"
          - db_type: "uuid"
            go_type: "github.com/google/uuid.UUID"
```

**`Taskfile.yml`** — extend `SQLC_DIRS` (the `sqlc:check` guard) to include the new gen dir:
```yaml
  SQLC_DIRS: internal/platform/db/dbgen internal/platform/events/eventsdb internal/modules/identity/repo/identitydb internal/modules/seller/repo/sellerdb internal/modules/catalog/repo/catalogdb
```

**`internal/modules/catalog/repo/queries/catalog.sql`**
```sql
-- ===== categories =====

-- name: InsertCategory :exec
INSERT INTO catalog_categories (id, name, slug, parent_id) VALUES ($1, $2, $3, $4);

-- name: ListCategories :many
SELECT id, name, slug, parent_id FROM catalog_categories
WHERE retired_at IS NULL ORDER BY name;

-- name: GetCategoryByID :one
SELECT id, name, slug, parent_id FROM catalog_categories
WHERE id = $1 AND retired_at IS NULL;

-- name: UpdateCategory :exec
UPDATE catalog_categories SET name = $2, parent_id = $3 WHERE id = $1 AND retired_at IS NULL;

-- name: RetireCategory :exec
UPDATE catalog_categories SET retired_at = now() WHERE id = $1 AND retired_at IS NULL;

-- name: FlagProductsForRecategorization :exec
UPDATE catalog_products SET needs_recategorization = true, updated_at = now() WHERE category_id = $1;

-- ===== products =====

-- name: InsertProduct :exec
INSERT INTO catalog_products
  (id, seller_id, category_id, title, description, status, base_price_minor, currency, has_variants, stock, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: GetProductByID :one
SELECT id, seller_id, category_id, title, description, status, base_price_minor, currency,
       has_variants, stock, needs_recategorization, created_at, updated_at
FROM catalog_products WHERE id = $1;

-- name: ListProductsBySeller :many
SELECT id, seller_id, category_id, title, description, status, base_price_minor, currency,
       has_variants, stock, needs_recategorization, created_at, updated_at
FROM catalog_products WHERE seller_id = $1 ORDER BY created_at DESC;

-- name: UpdateProduct :exec
UPDATE catalog_products
SET category_id = $2, title = $3, description = $4, status = $5, base_price_minor = $6,
    currency = $7, stock = $8, updated_at = now()
WHERE id = $1;

-- name: UpdateProductStatus :exec
UPDATE catalog_products SET status = $2, updated_at = now() WHERE id = $1;

-- name: SetProductHasVariants :exec
UPDATE catalog_products SET has_variants = $2, updated_at = now() WHERE id = $1;

-- name: DeleteProduct :exec
DELETE FROM catalog_products WHERE id = $1;

-- name: DeactivateActiveProductsBySeller :many
UPDATE catalog_products SET status = 'inactive', updated_at = now()
WHERE seller_id = $1 AND status = 'active'
RETURNING id;

-- ===== variants =====

-- name: InsertVariant :exec
INSERT INTO catalog_variants (id, product_id, color, size, sku, price_minor, stock)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetVariantByID :one
SELECT id, product_id, color, size, sku, price_minor, stock FROM catalog_variants WHERE id = $1;

-- name: ListVariantsByProduct :many
SELECT id, product_id, color, size, sku, price_minor, stock FROM catalog_variants
WHERE product_id = $1 ORDER BY created_at;

-- name: UpdateVariant :exec
UPDATE catalog_variants SET color = $2, size = $3, sku = $4, price_minor = $5, stock = $6 WHERE id = $1;

-- name: DeleteVariant :exec
DELETE FROM catalog_variants WHERE id = $1;

-- name: CountVariants :one
SELECT count(*) FROM catalog_variants WHERE product_id = $1;
```

> **sqlc output names** (from `task sqlc`): models `CatalogCategory` / `CatalogProduct` / `CatalogVariant`;
> param structs `InsertCategoryParams`, `UpdateCategoryParams{ID, Name, ParentID}`, `InsertProductParams`,
> `UpdateProductParams`, `UpdateProductStatusParams{ID, Status}`, `SetProductHasVariantsParams{ID, HasVariants}`,
> `InsertVariantParams`, `UpdateVariantParams`. Because `emit_pointers_for_null_types: true`, `ParentID`,
> `Color`, `Size` are `*uuid.UUID` / `*string`. The `:one`/`:many` selects read fixed column subsets, so each
> returns its own row struct (`GetProductByIDRow`, `ListProductsBySellerRow`, `GetVariantByIDRow`, …) with the
> listed fields; the repo maps them through positional helpers (`toProduct`, `toVariant`, `toCategory`) so the
> concrete row-type name does not matter. `DeactivateActiveProductsBySeller` returns `[]uuid.UUID`.

### New module: `catalog`

**`internal/modules/catalog/domain/catalog.go`**
```go
package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Status is the product lifecycle state (PRD FR-11; mirrors OpenAPI ProductStatus).
type Status string

const (
	StatusDraft    Status = "draft"
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
)

// SellerStatusApproved is the seller.Status value a seller must hold to write products. Compared as a string
// so catalog/domain need not import seller/domain.
const SellerStatusApproved = "approved"

func (s Status) Valid() bool {
	return s == StatusDraft || s == StatusActive || s == StatusInactive
}

// Category is an admin-managed grouping; ParentID is nil for a top-level category.
type Category struct {
	ID       uuid.UUID
	Name     string
	Slug     string
	ParentID *uuid.UUID
}

// Product belongs to exactly one seller and one category (FR-10). When HasVariants is true the variant rows
// carry price/stock and Stock here is unused (the sellable unit is the variant; §6).
type Product struct {
	ID                    uuid.UUID
	SellerID              uuid.UUID
	CategoryID            uuid.UUID
	Title                 string
	Description           string
	Status                Status
	BasePriceMinor        int64
	Currency              string
	HasVariants           bool
	Stock                 int32
	NeedsRecategorization bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Variant is a sellable unit on the optional color/size axes, with its own SKU, price, and stock (FR-13).
type Variant struct {
	ID         uuid.UUID
	ProductID  uuid.UUID
	Color      *string
	Size       *string
	SKU        string
	PriceMinor int64
	Stock      int32
}

// Sellable is the cross-module projection a sibling (P10 cart, P11 checkout) reads via catalog.Reader.
// VariantID is uuid.Nil for a variant-less product. Active mirrors product status == active.
type Sellable struct {
	ProductID  uuid.UUID
	VariantID  uuid.UUID
	SellerID   uuid.UUID
	PriceMinor int64
	Currency   string
	Stock      int32
	Active     bool
}

// Slugify produces a URL-safe slug from a category name (lowercase, spaces/underscores -> dashes).
func Slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, "_", " ")
	return strings.Join(strings.Fields(s), "-")
}
```

**`internal/modules/catalog/domain/events.go`**
```go
package domain

import "github.com/google/uuid"

// Events published (atomically, via the outbox) on a product's visibility edges (§6).
// Consumed by P10 cart (prune stale lines) and P16 notifications.
const (
	EventProductPublished   = "ProductPublished"
	EventProductUnpublished = "ProductUnpublished"
)

type ProductPublishedPayload struct {
	ProductID uuid.UUID `json:"product_id"`
	SellerID  uuid.UUID `json:"seller_id"`
}

type ProductUnpublishedPayload struct {
	ProductID uuid.UUID `json:"product_id"`
	SellerID  uuid.UUID `json:"seller_id"`
}
```

**`internal/modules/catalog/domain/errors.go`**
```go
package domain

import "errors"

var (
	ErrCategoryNotFound   = errors.New("category not found")
	ErrProductNotFound    = errors.New("product not found")
	ErrVariantNotFound    = errors.New("variant not found")
	ErrNotOwner           = errors.New("product belongs to another seller")
	ErrSellerNotApproved  = errors.New("seller is not approved")
	ErrInvalidStatus      = errors.New("invalid product status")
	ErrVariantOnVariantless = errors.New("cannot add a variant to a variant-less sellable unit after stock is set") // reserved
)
```

**`internal/modules/catalog/port.go`**
```go
// Package catalog is the public port surface of the catalog module: the events it publishes and the Reader
// port other modules (P10 cart, P11 checkout) consume to resolve a sellable unit's price/stock by id.
package catalog

import (
	"context"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/modules/catalog/domain"
)

// Published events (the single public surface for producers/consumers).
const (
	EventProductPublished   = domain.EventProductPublished
	EventProductUnpublished = domain.EventProductUnpublished
)

// Payload + projection aliases so consumers decode/read without importing catalog/domain.
type (
	ProductPublishedPayload   = domain.ProductPublishedPayload
	ProductUnpublishedPayload = domain.ProductUnpublishedPayload
	Sellable                  = domain.Sellable
)

// Reader is the read port sibling modules consume to price/availability-check a sellable unit. VariantID is
// uuid.Nil for a variant-less product. They import ONLY this file. *service.Service satisfies it.
type Reader interface {
	GetSellable(ctx context.Context, productID, variantID uuid.UUID) (Sellable, error)
}
```

**`internal/modules/catalog/service/ports.go`**
```go
package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/catalog/domain"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

// Repository is the persistence port the service needs; repo/ implements it over sqlc. Write methods take
// pgx.Tx so the service composes them with the outbox in one RunInTx; read methods take ctx and return
// pgx.ErrNoRows when absent (the service maps that to domain errors).
type Repository interface {
	// categories
	InsertCategory(ctx context.Context, tx pgx.Tx, c domain.Category) error
	ListCategories(ctx context.Context) ([]domain.Category, error)
	GetCategoryByID(ctx context.Context, id uuid.UUID) (domain.Category, error)
	UpdateCategory(ctx context.Context, tx pgx.Tx, c domain.Category) error
	RetireCategory(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
	FlagProductsForRecategorization(ctx context.Context, tx pgx.Tx, categoryID uuid.UUID) error

	// products
	InsertProduct(ctx context.Context, tx pgx.Tx, p domain.Product) error
	GetProductByID(ctx context.Context, id uuid.UUID) (domain.Product, error)
	ListProductsBySeller(ctx context.Context, sellerID uuid.UUID) ([]domain.Product, error)
	UpdateProduct(ctx context.Context, tx pgx.Tx, p domain.Product) error
	UpdateProductStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error
	SetProductHasVariants(ctx context.Context, tx pgx.Tx, id uuid.UUID, has bool) error
	DeleteProduct(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
	DeactivateActiveProductsBySeller(ctx context.Context, tx pgx.Tx, sellerID uuid.UUID) ([]uuid.UUID, error)

	// variants
	InsertVariant(ctx context.Context, tx pgx.Tx, v domain.Variant) error
	GetVariantByID(ctx context.Context, id uuid.UUID) (domain.Variant, error)
	ListVariantsByProduct(ctx context.Context, productID uuid.UUID) ([]domain.Variant, error)
	UpdateVariant(ctx context.Context, tx pgx.Tx, v domain.Variant) error
	DeleteVariant(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
	CountVariants(ctx context.Context, productID uuid.UUID) (int64, error)
}

// Outbox is the publish port (satisfied by *events.Outbox) — kept narrow for testability.
type Outbox interface {
	Write(ctx context.Context, tx pgx.Tx, e events.Event) error
}
```

**`internal/modules/catalog/service/service.go`**
```go
package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	seller "github.com/amoorihesham/eco-api/internal/modules/seller"
	"github.com/amoorihesham/eco-api/internal/modules/catalog/domain"
	"github.com/amoorihesham/eco-api/internal/platform/db"
)

// Service implements the catalog use cases. It depends only on ports: its Repository, the Outbox, and the
// seller.Reader (P5) for the synchronous "is this seller approved?" gate on writes.
type Service struct {
	pool    db.Beginner
	repo    Repository
	sellers seller.Reader
	outbox  Outbox
}

func New(pool db.Beginner, repo Repository, sellers seller.Reader, outbox Outbox) *Service {
	return &Service{pool: pool, repo: repo, sellers: sellers, outbox: outbox}
}

// --- inputs (editable fields; ids/timestamps are server-owned) ---

type CategoryInput struct {
	Name     string
	ParentID *uuid.UUID
}

type ProductInput struct {
	CategoryID     uuid.UUID
	Title          string
	Description    string
	BasePriceMinor int64
	Currency       string
	Status         domain.Status // defaults to draft when empty
	Stock          int32
}

type VariantInput struct {
	Color      *string
	Size       *string
	SKU        string
	PriceMinor int64
	Stock      int32
}

// ProductView bundles a product with its variants for the owner read-back.
type ProductView struct {
	Product  domain.Product
	Variants []domain.Variant
}

// ===== categories (admin) =====

func (s *Service) CreateCategory(ctx context.Context, in CategoryInput) (domain.Category, error) {
	c := domain.Category{ID: uuid.New(), Name: in.Name, Slug: domain.Slugify(in.Name), ParentID: in.ParentID}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.InsertCategory(ctx, tx, c)
	}); err != nil {
		return domain.Category{}, err
	}
	return c, nil
}

func (s *Service) ListCategories(ctx context.Context) ([]domain.Category, error) {
	return s.repo.ListCategories(ctx)
}

func (s *Service) UpdateCategory(ctx context.Context, id uuid.UUID, in CategoryInput) (domain.Category, error) {
	if _, err := s.getCategory(ctx, id); err != nil {
		return domain.Category{}, err
	}
	c := domain.Category{ID: id, Name: in.Name, Slug: domain.Slugify(in.Name), ParentID: in.ParentID}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.UpdateCategory(ctx, tx, c)
	}); err != nil {
		return domain.Category{}, err
	}
	return c, nil
}

// RetireCategory soft-retires a category and flags its products for re-categorization — never orphans them
// (FR-41). Both writes commit together.
func (s *Service) RetireCategory(ctx context.Context, id uuid.UUID) error {
	if _, err := s.getCategory(ctx, id); err != nil {
		return err
	}
	return db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.RetireCategory(ctx, tx, id); err != nil {
			return err
		}
		return s.repo.FlagProductsForRecategorization(ctx, tx, id)
	})
}

// ===== products (seller, owner) =====

// CreateProduct creates a product for an APPROVED seller. If it is created directly as active, ProductPublished
// is published atomically.
func (s *Service) CreateProduct(ctx context.Context, sellerID uuid.UUID, in ProductInput) (domain.Product, error) {
	if err := s.ensureApproved(ctx, sellerID); err != nil {
		return domain.Product{}, err
	}
	status := in.Status
	if status == "" {
		status = domain.StatusDraft
	}
	if !status.Valid() {
		return domain.Product{}, domain.ErrInvalidStatus
	}
	if _, err := s.getCategory(ctx, in.CategoryID); err != nil {
		return domain.Product{}, err
	}
	now := time.Now().UTC()
	p := domain.Product{
		ID: uuid.New(), SellerID: sellerID, CategoryID: in.CategoryID,
		Title: in.Title, Description: in.Description, Status: status,
		BasePriceMinor: in.BasePriceMinor, Currency: in.Currency,
		HasVariants: false, Stock: in.Stock, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.InsertProduct(ctx, tx, p); err != nil {
			return err
		}
		return s.emitVisibility(ctx, tx, p.ID, p.SellerID, domain.StatusDraft, p.Status)
	}); err != nil {
		return domain.Product{}, err
	}
	return p, nil
}

// GetProduct returns a product + its variants for the owner or an admin. Callers that are neither get a 404
// at the handler (public discovery is P9).
func (s *Service) GetProduct(ctx context.Context, id uuid.UUID) (ProductView, error) {
	p, err := s.getProduct(ctx, id)
	if err != nil {
		return ProductView{}, err
	}
	vs, err := s.repo.ListVariantsByProduct(ctx, id)
	if err != nil {
		return ProductView{}, err
	}
	return ProductView{Product: p, Variants: vs}, nil
}

func (s *Service) ListMyProducts(ctx context.Context, sellerID uuid.UUID) ([]domain.Product, error) {
	return s.repo.ListProductsBySeller(ctx, sellerID)
}

// UpdateProduct edits an owned product and publishes the visibility event for any status edge.
func (s *Service) UpdateProduct(ctx context.Context, sellerID, id uuid.UUID, in ProductInput) (domain.Product, error) {
	p, err := s.ownedProduct(ctx, sellerID, id)
	if err != nil {
		return domain.Product{}, err
	}
	status := in.Status
	if status == "" {
		status = p.Status
	}
	if !status.Valid() {
		return domain.Product{}, domain.ErrInvalidStatus
	}
	if in.CategoryID != p.CategoryID {
		if _, err := s.getCategory(ctx, in.CategoryID); err != nil {
			return domain.Product{}, err
		}
	}
	old := p.Status
	p.CategoryID, p.Title, p.Description = in.CategoryID, in.Title, in.Description
	p.Status, p.BasePriceMinor, p.Currency, p.Stock = status, in.BasePriceMinor, in.Currency, in.Stock
	p.NeedsRecategorization = false // a fresh category clears the flag
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.UpdateProduct(ctx, tx, p); err != nil {
			return err
		}
		return s.emitVisibility(ctx, tx, p.ID, p.SellerID, old, p.Status)
	}); err != nil {
		return domain.Product{}, err
	}
	return p, nil
}

// DeleteProduct removes an owned product (variants cascade); if it was active, ProductUnpublished is emitted.
func (s *Service) DeleteProduct(ctx context.Context, sellerID, id uuid.UUID) error {
	p, err := s.ownedProduct(ctx, sellerID, id)
	if err != nil {
		return err
	}
	return db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.DeleteProduct(ctx, tx, id); err != nil {
			return err
		}
		return s.emitVisibility(ctx, tx, p.ID, p.SellerID, p.Status, domain.StatusInactive)
	})
}

// UnpublishProduct (admin) forces a product inactive; emits ProductUnpublished only if it was active.
func (s *Service) UnpublishProduct(ctx context.Context, id uuid.UUID) (domain.Product, error) {
	p, err := s.getProduct(ctx, id)
	if err != nil {
		return domain.Product{}, err
	}
	old := p.Status
	p.Status = domain.StatusInactive
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.UpdateProductStatus(ctx, tx, id, string(domain.StatusInactive)); err != nil {
			return err
		}
		return s.emitVisibility(ctx, tx, p.ID, p.SellerID, old, p.Status)
	}); err != nil {
		return domain.Product{}, err
	}
	return p, nil
}

// ===== variants (seller, owner) =====

func (s *Service) AddVariant(ctx context.Context, sellerID, productID uuid.UUID, in VariantInput) (domain.Variant, error) {
	if _, err := s.ownedProduct(ctx, sellerID, productID); err != nil {
		return domain.Variant{}, err
	}
	v := domain.Variant{
		ID: uuid.New(), ProductID: productID, Color: in.Color, Size: in.Size,
		SKU: in.SKU, PriceMinor: in.PriceMinor, Stock: in.Stock,
	}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.InsertVariant(ctx, tx, v); err != nil {
			return err
		}
		return s.repo.SetProductHasVariants(ctx, tx, productID, true) // first variant flips the unit (§6)
	}); err != nil {
		return domain.Variant{}, err
	}
	return v, nil
}

func (s *Service) UpdateVariant(ctx context.Context, sellerID, productID, variantID uuid.UUID, in VariantInput) (domain.Variant, error) {
	if _, err := s.ownedProduct(ctx, sellerID, productID); err != nil {
		return domain.Variant{}, err
	}
	if _, err := s.getVariant(ctx, variantID); err != nil {
		return domain.Variant{}, err
	}
	v := domain.Variant{
		ID: variantID, ProductID: productID, Color: in.Color, Size: in.Size,
		SKU: in.SKU, PriceMinor: in.PriceMinor, Stock: in.Stock,
	}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.UpdateVariant(ctx, tx, v)
	}); err != nil {
		return domain.Variant{}, err
	}
	return v, nil
}

func (s *Service) DeleteVariant(ctx context.Context, sellerID, productID, variantID uuid.UUID) error {
	if _, err := s.ownedProduct(ctx, sellerID, productID); err != nil {
		return err
	}
	return db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.DeleteVariant(ctx, tx, variantID); err != nil {
			return err
		}
		n, err := s.repo.CountVariants(ctx, productID)
		if err != nil {
			return err
		}
		if n == 0 { // last variant gone — the product is variant-less again
			return s.repo.SetProductHasVariants(ctx, tx, productID, false)
		}
		return nil
	})
}

// ===== shared helpers =====

// ensureApproved gates a seller write on the seller's status (P5 port). A real DB error surfaces as 500; a
// non-approved status (e.g. suspended) surfaces as ErrSellerNotApproved (403).
func (s *Service) ensureApproved(ctx context.Context, sellerID uuid.UUID) error {
	st, err := s.sellers.SellerStatus(ctx, sellerID)
	if err != nil {
		return err
	}
	if string(st) != domain.SellerStatusApproved {
		return domain.ErrSellerNotApproved
	}
	return nil
}

func (s *Service) ownedProduct(ctx context.Context, sellerID, id uuid.UUID) (domain.Product, error) {
	if err := s.ensureApproved(ctx, sellerID); err != nil {
		return domain.Product{}, err
	}
	p, err := s.getProduct(ctx, id)
	if err != nil {
		return domain.Product{}, err
	}
	if p.SellerID != sellerID {
		return domain.Product{}, domain.ErrNotOwner
	}
	return p, nil
}

func (s *Service) getProduct(ctx context.Context, id uuid.UUID) (domain.Product, error) {
	p, err := s.repo.GetProductByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Product{}, domain.ErrProductNotFound
		}
		return domain.Product{}, err
	}
	return p, nil
}

func (s *Service) getCategory(ctx context.Context, id uuid.UUID) (domain.Category, error) {
	c, err := s.repo.GetCategoryByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Category{}, domain.ErrCategoryNotFound
		}
		return domain.Category{}, err
	}
	return c, nil
}

func (s *Service) getVariant(ctx context.Context, id uuid.UUID) (domain.Variant, error) {
	v, err := s.repo.GetVariantByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Variant{}, domain.ErrVariantNotFound
		}
		return domain.Variant{}, err
	}
	return v, nil
}
```

**`internal/modules/catalog/service/visibility.go`**
```go
package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/catalog/domain"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

// emitVisibility publishes the event for a status edge (§6), on the SAME tx as the write, so state + event
// commit atomically. Non-visibility edges emit nothing.
func (s *Service) emitVisibility(ctx context.Context, tx pgx.Tx, productID, sellerID uuid.UUID, old, now domain.Status) error {
	switch {
	case old != domain.StatusActive && now == domain.StatusActive:
		evt, err := events.NewEvent(domain.EventProductPublished,
			domain.ProductPublishedPayload{ProductID: productID, SellerID: sellerID})
		if err != nil {
			return err
		}
		return s.outbox.Write(ctx, tx, evt)
	case old == domain.StatusActive && now != domain.StatusActive:
		return s.publishUnpublished(ctx, tx, productID, sellerID)
	default:
		return nil
	}
}

func (s *Service) publishUnpublished(ctx context.Context, tx pgx.Tx, productID, sellerID uuid.UUID) error {
	evt, err := events.NewEvent(domain.EventProductUnpublished,
		domain.ProductUnpublishedPayload{ProductID: productID, SellerID: sellerID})
	if err != nil {
		return err
	}
	return s.outbox.Write(ctx, tx, evt)
}

// HideSellerProducts is the SellerSuspended consumer (wired in cmd/api/main.go). It deactivates the seller's
// active products and publishes one ProductUnpublished per product — all on the dedupe tx supplied by
// events.Idempotent, so the effect and the processed-events mark commit together. Idempotent: a replay finds
// no active products and is a no-op.
func (s *Service) HideSellerProducts(ctx context.Context, tx pgx.Tx, sellerID uuid.UUID) error {
	ids, err := s.repo.DeactivateActiveProductsBySeller(ctx, tx, sellerID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.publishUnpublished(ctx, tx, id, sellerID); err != nil {
			return err
		}
	}
	return nil
}
```

**`internal/modules/catalog/service/reader.go`**
```go
package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/modules/catalog/domain"
)

// GetSellable satisfies catalog.Reader — P10 cart / P11 checkout resolve a sellable unit's price/stock by id.
// variantID == uuid.Nil selects the variant-less product itself (§6).
func (s *Service) GetSellable(ctx context.Context, productID, variantID uuid.UUID) (domain.Sellable, error) {
	p, err := s.getProduct(ctx, productID)
	if err != nil {
		return domain.Sellable{}, err
	}
	out := domain.Sellable{
		ProductID: p.ID, SellerID: p.SellerID, Currency: p.Currency,
		Active: p.Status == domain.StatusActive,
	}
	if variantID == uuid.Nil {
		out.PriceMinor, out.Stock = p.BasePriceMinor, p.Stock
		return out, nil
	}
	v, err := s.getVariant(ctx, variantID)
	if err != nil {
		return domain.Sellable{}, err
	}
	if v.ProductID != productID {
		return domain.Sellable{}, domain.ErrVariantNotFound
	}
	out.VariantID, out.PriceMinor, out.Stock = v.ID, v.PriceMinor, v.Stock
	return out, nil
}
```

**`internal/modules/catalog/repo/repo.go`**
```go
package repo

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amoorihesham/eco-api/internal/modules/catalog/domain"
	"github.com/amoorihesham/eco-api/internal/modules/catalog/repo/catalogdb"
)

// Repo implements service.Repository over sqlc-generated queries.
type Repo struct{ q *catalogdb.Queries }

func New(pool *pgxpool.Pool) *Repo { return &Repo{q: catalogdb.New(pool)} }

// ===== categories =====

func (r *Repo) InsertCategory(ctx context.Context, tx pgx.Tx, c domain.Category) error {
	return r.q.WithTx(tx).InsertCategory(ctx, catalogdb.InsertCategoryParams{
		ID: c.ID, Name: c.Name, Slug: c.Slug, ParentID: c.ParentID,
	})
}

func (r *Repo) ListCategories(ctx context.Context) ([]domain.Category, error) {
	rows, err := r.q.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Category, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.Category{ID: row.ID, Name: row.Name, Slug: row.Slug, ParentID: row.ParentID})
	}
	return out, nil
}

func (r *Repo) GetCategoryByID(ctx context.Context, id uuid.UUID) (domain.Category, error) {
	row, err := r.q.GetCategoryByID(ctx, id)
	if err != nil {
		return domain.Category{}, err
	}
	return domain.Category{ID: row.ID, Name: row.Name, Slug: row.Slug, ParentID: row.ParentID}, nil
}

func (r *Repo) UpdateCategory(ctx context.Context, tx pgx.Tx, c domain.Category) error {
	return r.q.WithTx(tx).UpdateCategory(ctx, catalogdb.UpdateCategoryParams{ID: c.ID, Name: c.Name, ParentID: c.ParentID})
}

func (r *Repo) RetireCategory(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	return r.q.WithTx(tx).RetireCategory(ctx, id)
}

func (r *Repo) FlagProductsForRecategorization(ctx context.Context, tx pgx.Tx, categoryID uuid.UUID) error {
	return r.q.WithTx(tx).FlagProductsForRecategorization(ctx, categoryID)
}

// ===== products =====

func (r *Repo) InsertProduct(ctx context.Context, tx pgx.Tx, p domain.Product) error {
	return r.q.WithTx(tx).InsertProduct(ctx, catalogdb.InsertProductParams{
		ID: p.ID, SellerID: p.SellerID, CategoryID: p.CategoryID, Title: p.Title, Description: p.Description,
		Status: string(p.Status), BasePriceMinor: p.BasePriceMinor, Currency: p.Currency,
		HasVariants: p.HasVariants, Stock: p.Stock, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	})
}

func (r *Repo) GetProductByID(ctx context.Context, id uuid.UUID) (domain.Product, error) {
	row, err := r.q.GetProductByID(ctx, id)
	if err != nil {
		return domain.Product{}, err
	}
	return toProduct(row.ID, row.SellerID, row.CategoryID, row.Title, row.Description, row.Status,
		row.BasePriceMinor, row.Currency, row.HasVariants, row.Stock, row.NeedsRecategorization,
		row.CreatedAt, row.UpdatedAt), nil
}

func (r *Repo) ListProductsBySeller(ctx context.Context, sellerID uuid.UUID) ([]domain.Product, error) {
	rows, err := r.q.ListProductsBySeller(ctx, sellerID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Product, 0, len(rows))
	for _, row := range rows {
		out = append(out, toProduct(row.ID, row.SellerID, row.CategoryID, row.Title, row.Description, row.Status,
			row.BasePriceMinor, row.Currency, row.HasVariants, row.Stock, row.NeedsRecategorization,
			row.CreatedAt, row.UpdatedAt))
	}
	return out, nil
}

func (r *Repo) UpdateProduct(ctx context.Context, tx pgx.Tx, p domain.Product) error {
	return r.q.WithTx(tx).UpdateProduct(ctx, catalogdb.UpdateProductParams{
		ID: p.ID, CategoryID: p.CategoryID, Title: p.Title, Description: p.Description,
		Status: string(p.Status), BasePriceMinor: p.BasePriceMinor, Currency: p.Currency, Stock: p.Stock,
	})
}

func (r *Repo) UpdateProductStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error {
	return r.q.WithTx(tx).UpdateProductStatus(ctx, catalogdb.UpdateProductStatusParams{ID: id, Status: status})
}

func (r *Repo) SetProductHasVariants(ctx context.Context, tx pgx.Tx, id uuid.UUID, has bool) error {
	return r.q.WithTx(tx).SetProductHasVariants(ctx, catalogdb.SetProductHasVariantsParams{ID: id, HasVariants: has})
}

func (r *Repo) DeleteProduct(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	return r.q.WithTx(tx).DeleteProduct(ctx, id)
}

func (r *Repo) DeactivateActiveProductsBySeller(ctx context.Context, tx pgx.Tx, sellerID uuid.UUID) ([]uuid.UUID, error) {
	return r.q.WithTx(tx).DeactivateActiveProductsBySeller(ctx, sellerID)
}

// ===== variants =====

func (r *Repo) InsertVariant(ctx context.Context, tx pgx.Tx, v domain.Variant) error {
	return r.q.WithTx(tx).InsertVariant(ctx, catalogdb.InsertVariantParams{
		ID: v.ID, ProductID: v.ProductID, Color: v.Color, Size: v.Size,
		Sku: v.SKU, PriceMinor: v.PriceMinor, Stock: v.Stock,
	})
}

func (r *Repo) GetVariantByID(ctx context.Context, id uuid.UUID) (domain.Variant, error) {
	row, err := r.q.GetVariantByID(ctx, id)
	if err != nil {
		return domain.Variant{}, err
	}
	return toVariant(row.ID, row.ProductID, row.Color, row.Size, row.Sku, row.PriceMinor, row.Stock), nil
}

func (r *Repo) ListVariantsByProduct(ctx context.Context, productID uuid.UUID) ([]domain.Variant, error) {
	rows, err := r.q.ListVariantsByProduct(ctx, productID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Variant, 0, len(rows))
	for _, row := range rows {
		out = append(out, toVariant(row.ID, row.ProductID, row.Color, row.Size, row.Sku, row.PriceMinor, row.Stock))
	}
	return out, nil
}

func (r *Repo) UpdateVariant(ctx context.Context, tx pgx.Tx, v domain.Variant) error {
	return r.q.WithTx(tx).UpdateVariant(ctx, catalogdb.UpdateVariantParams{
		ID: v.ID, Color: v.Color, Size: v.Size, Sku: v.SKU, PriceMinor: v.PriceMinor, Stock: v.Stock,
	})
}

func (r *Repo) DeleteVariant(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	return r.q.WithTx(tx).DeleteVariant(ctx, id)
}

func (r *Repo) CountVariants(ctx context.Context, productID uuid.UUID) (int64, error) {
	return r.q.CountVariants(ctx, productID)
}

// ===== row -> domain helpers =====

func toProduct(id, sellerID, categoryID uuid.UUID, title, description, status string, basePrice int64,
	currency string, hasVariants bool, stock int32, needsRecat bool, createdAt, updatedAt timeTime) domain.Product {
	return domain.Product{
		ID: id, SellerID: sellerID, CategoryID: categoryID, Title: title, Description: description,
		Status: domain.Status(status), BasePriceMinor: basePrice, Currency: currency, HasVariants: hasVariants,
		Stock: stock, NeedsRecategorization: needsRecat, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}

func toVariant(id, productID uuid.UUID, color, size *string, sku string, price int64, stock int32) domain.Variant {
	return domain.Variant{ID: id, ProductID: productID, Color: color, Size: size, SKU: sku, PriceMinor: price, Stock: stock}
}
```

> The helper signature above uses `timeTime` as shorthand for `time.Time` — when you type the file, import
> `"time"` and replace `timeTime` with `time.Time` (kept distinct here only to flag the one stdlib import the
> repo needs beyond the drivers).

**`internal/modules/catalog/handler/handler.go`**
```go
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/modules/catalog/domain"
	"github.com/amoorihesham/eco-api/internal/modules/catalog/service"
	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

type Handler struct{ svc *service.Service }

func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

// --- DTOs (mirror the OpenAPI Catalog schemas) ---

type categoryInput struct {
	Name     string     `json:"name"`
	ParentID *uuid.UUID `json:"parent_id"`
}

type categoryDTO struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Slug     string  `json:"slug"`
	ParentID *string `json:"parent_id"`
}

type productInput struct {
	CategoryID     string `json:"category_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	BasePriceMinor int64  `json:"base_price_minor"`
	Currency       string `json:"currency"`
	Status         string `json:"status"`
	Stock          int32  `json:"stock"`
}

type variantInput struct {
	Color      *string `json:"color"`
	Size       *string `json:"size"`
	SKU        string  `json:"sku"`
	PriceMinor int64   `json:"price_minor"`
	Stock      int32   `json:"stock"`
}

type variantDTO struct {
	ID         string  `json:"id"`
	ProductID  string  `json:"product_id"`
	Color      *string `json:"color"`
	Size       *string `json:"size"`
	SKU        string  `json:"sku"`
	PriceMinor int64   `json:"price_minor"`
	Stock      int32   `json:"stock"`
}

type productDTO struct {
	ID             string       `json:"id"`
	SellerID       string       `json:"seller_id"`
	CategoryID     string       `json:"category_id"`
	Title          string       `json:"title"`
	Description    string       `json:"description,omitempty"`
	Status         string       `json:"status"`
	BasePriceMinor int64        `json:"base_price_minor"`
	Currency       string       `json:"currency"`
	HasVariants    bool         `json:"has_variants"`
	Stock          *int32       `json:"stock"` // null when has_variants
	Variants       []variantDTO `json:"variants"`
	CreatedAt      string       `json:"created_at"`
}

// --- categories ---

func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request) {
	cs, err := h.svc.ListCategories(r.Context())
	if err != nil {
		httpx.Internal(w, "could not list categories")
		return
	}
	out := make([]categoryDTO, 0, len(cs))
	for _, c := range cs {
		out = append(out, toCategoryDTO(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (h *Handler) createCategory(w http.ResponseWriter, r *http.Request) {
	var req categoryInput
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed",
			httpx.ErrorDetail{Field: "name", Message: "name is required"})
		return
	}
	c, err := h.svc.CreateCategory(r.Context(), service.CategoryInput{Name: strings.TrimSpace(req.Name), ParentID: req.ParentID})
	if err != nil {
		writeCatalogError(w, err, "could not create category")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toCategoryDTO(c))
}

func (h *Handler) updateCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "categoryId", "category not found")
	if !ok {
		return
	}
	var req categoryInput
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed",
			httpx.ErrorDetail{Field: "name", Message: "name is required"})
		return
	}
	c, err := h.svc.UpdateCategory(r.Context(), id, service.CategoryInput{Name: strings.TrimSpace(req.Name), ParentID: req.ParentID})
	if err != nil {
		writeCatalogError(w, err, "could not update category")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toCategoryDTO(c))
}

func (h *Handler) deleteCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "categoryId", "category not found")
	if !ok {
		return
	}
	if err := h.svc.RetireCategory(r.Context(), id); err != nil {
		writeCatalogError(w, err, "could not retire category")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- products (seller) ---

func (h *Handler) createProduct(w http.ResponseWriter, r *http.Request) {
	sellerID, ok := callerID(w, r)
	if !ok {
		return
	}
	var req productInput
	if !decode(w, r, &req) {
		return
	}
	in, errs := toProductInput(req)
	if len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	p, err := h.svc.CreateProduct(r.Context(), sellerID, in)
	if err != nil {
		writeCatalogError(w, err, "could not create product")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toProductDTO(p, nil))
}

func (h *Handler) updateProduct(w http.ResponseWriter, r *http.Request) {
	sellerID, ok := callerID(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "productId", "product not found")
	if !ok {
		return
	}
	var req productInput
	if !decode(w, r, &req) {
		return
	}
	in, errs := toProductInput(req)
	if len(errs) > 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed", errs...)
		return
	}
	p, err := h.svc.UpdateProduct(r.Context(), sellerID, id, in)
	if err != nil {
		writeCatalogError(w, err, "could not update product")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toProductDTO(p, nil))
}

func (h *Handler) deleteProduct(w http.ResponseWriter, r *http.Request) {
	sellerID, ok := callerID(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "productId", "product not found")
	if !ok {
		return
	}
	if err := h.svc.DeleteProduct(r.Context(), sellerID, id); err != nil {
		writeCatalogError(w, err, "could not delete product")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getProduct is the owner read-back: only the owner (or an admin) may read; others get 404 (P9 owns public).
func (h *Handler) getProduct(w http.ResponseWriter, r *http.Request) {
	callerIDv, ok := callerID(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "productId", "product not found")
	if !ok {
		return
	}
	view, err := h.svc.GetProduct(r.Context(), id)
	if err != nil {
		writeCatalogError(w, err, "could not load product")
		return
	}
	role, _ := auth.Role(r.Context())
	if view.Product.SellerID != callerIDv && role != "admin" {
		httpx.NotFound(w, "product not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toProductDTO(view.Product, view.Variants))
}

func (h *Handler) listMyProducts(w http.ResponseWriter, r *http.Request) {
	sellerID, ok := callerID(w, r)
	if !ok {
		return
	}
	ps, err := h.svc.ListMyProducts(r.Context(), sellerID)
	if err != nil {
		httpx.Internal(w, "could not list products")
		return
	}
	out := make([]productDTO, 0, len(ps))
	for _, p := range ps {
		out = append(out, toProductDTO(p, nil))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- variants (seller) ---

func (h *Handler) addVariant(w http.ResponseWriter, r *http.Request) {
	sellerID, ok := callerID(w, r)
	if !ok {
		return
	}
	productID, ok := pathID(w, r, "productId", "product not found")
	if !ok {
		return
	}
	var req variantInput
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SKU) == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed",
			httpx.ErrorDetail{Field: "sku", Message: "sku is required"})
		return
	}
	v, err := h.svc.AddVariant(r.Context(), sellerID, productID, toVariantInput(req))
	if err != nil {
		writeCatalogError(w, err, "could not add variant")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toVariantDTO(v))
}

func (h *Handler) updateVariant(w http.ResponseWriter, r *http.Request) {
	sellerID, ok := callerID(w, r)
	if !ok {
		return
	}
	productID, ok := pathID(w, r, "productId", "product not found")
	if !ok {
		return
	}
	variantID, ok := pathID(w, r, "variantId", "variant not found")
	if !ok {
		return
	}
	var req variantInput
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SKU) == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "validation failed",
			httpx.ErrorDetail{Field: "sku", Message: "sku is required"})
		return
	}
	v, err := h.svc.UpdateVariant(r.Context(), sellerID, productID, variantID, toVariantInput(req))
	if err != nil {
		writeCatalogError(w, err, "could not update variant")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toVariantDTO(v))
}

func (h *Handler) deleteVariant(w http.ResponseWriter, r *http.Request) {
	sellerID, ok := callerID(w, r)
	if !ok {
		return
	}
	productID, ok := pathID(w, r, "productId", "product not found")
	if !ok {
		return
	}
	variantID, ok := pathID(w, r, "variantId", "variant not found")
	if !ok {
		return
	}
	if err := h.svc.DeleteVariant(r.Context(), sellerID, productID, variantID); err != nil {
		writeCatalogError(w, err, "could not delete variant")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- admin ---

func (h *Handler) unpublishProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "productId", "product not found")
	if !ok {
		return
	}
	p, err := h.svc.UnpublishProduct(r.Context(), id)
	if err != nil {
		writeCatalogError(w, err, "could not unpublish product")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toProductDTO(p, nil))
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

func pathID(w http.ResponseWriter, r *http.Request, name, notFound string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		httpx.NotFound(w, notFound)
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

func toProductInput(req productInput) (service.ProductInput, []httpx.ErrorDetail) {
	var errs []httpx.ErrorDetail
	if strings.TrimSpace(req.Title) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "title", Message: "title is required"})
	}
	if strings.TrimSpace(req.Currency) == "" {
		errs = append(errs, httpx.ErrorDetail{Field: "currency", Message: "currency is required"})
	}
	if req.BasePriceMinor < 0 {
		errs = append(errs, httpx.ErrorDetail{Field: "base_price_minor", Message: "must be >= 0"})
	}
	cat, err := uuid.Parse(req.CategoryID)
	if err != nil {
		errs = append(errs, httpx.ErrorDetail{Field: "category_id", Message: "must be a valid uuid"})
	}
	if len(errs) > 0 {
		return service.ProductInput{}, errs
	}
	return service.ProductInput{
		CategoryID: cat, Title: strings.TrimSpace(req.Title), Description: strings.TrimSpace(req.Description),
		BasePriceMinor: req.BasePriceMinor, Currency: strings.TrimSpace(req.Currency),
		Status: domain.Status(strings.TrimSpace(req.Status)), Stock: req.Stock,
	}, nil
}

func toVariantInput(req variantInput) service.VariantInput {
	return service.VariantInput{
		Color: req.Color, Size: req.Size, SKU: strings.TrimSpace(req.SKU),
		PriceMinor: req.PriceMinor, Stock: req.Stock,
	}
}

// writeCatalogError maps domain sentinels to the standard envelope.
func writeCatalogError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, domain.ErrCategoryNotFound), errors.Is(err, domain.ErrProductNotFound), errors.Is(err, domain.ErrVariantNotFound):
		httpx.NotFound(w, "not found")
	case errors.Is(err, domain.ErrSellerNotApproved):
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "seller is not approved")
	case errors.Is(err, domain.ErrNotOwner):
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "you do not own this product")
	case errors.Is(err, domain.ErrInvalidStatus):
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeValidation, "invalid product status")
	default:
		httpx.Internal(w, fallback)
	}
}

func toCategoryDTO(c domain.Category) categoryDTO {
	var parent *string
	if c.ParentID != nil {
		s := c.ParentID.String()
		parent = &s
	}
	return categoryDTO{ID: c.ID.String(), Name: c.Name, Slug: c.Slug, ParentID: parent}
}

func toVariantDTO(v domain.Variant) variantDTO {
	return variantDTO{
		ID: v.ID.String(), ProductID: v.ProductID.String(), Color: v.Color, Size: v.Size,
		SKU: v.SKU, PriceMinor: v.PriceMinor, Stock: v.Stock,
	}
}

func toProductDTO(p domain.Product, variants []domain.Variant) productDTO {
	dto := productDTO{
		ID: p.ID.String(), SellerID: p.SellerID.String(), CategoryID: p.CategoryID.String(),
		Title: p.Title, Description: p.Description, Status: string(p.Status),
		BasePriceMinor: p.BasePriceMinor, Currency: p.Currency, HasVariants: p.HasVariants,
		Variants:  make([]variantDTO, 0, len(variants)),
		CreatedAt: p.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if !p.HasVariants { // stock is null for variant products (the variant carries it)
		stock := p.Stock
		dto.Stock = &stock
	}
	for _, v := range variants {
		dto.Variants = append(dto.Variants, toVariantDTO(v))
	}
	return dto
}
```

**`internal/modules/catalog/handler/routes.go`**
```go
package handler

import (
	"net/http"

	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// Mount registers the catalog routes under /api/v1. Category reads are public; category writes + product
// unpublish need the admin role; product/variant writes and the owner read-back need the seller role. The
// caller id always comes from the verified token (auth.UserID), never the body/path.
func (h *Handler) Mount(mux *http.ServeMux, authn httpx.Middleware) {
	seller := func(next http.Handler) http.Handler { return authn(auth.RequireRole("seller")(next)) }
	admin := func(next http.Handler) http.Handler { return authn(auth.RequireRole("admin")(next)) }

	// Categories — public list; admin writes (RBAC on the owning module — §6)
	mux.HandleFunc("GET /api/v1/categories", h.listCategories)
	mux.Handle("POST /api/v1/categories", admin(http.HandlerFunc(h.createCategory)))
	mux.Handle("PATCH /api/v1/categories/{categoryId}", admin(http.HandlerFunc(h.updateCategory)))
	mux.Handle("DELETE /api/v1/categories/{categoryId}", admin(http.HandlerFunc(h.deleteCategory)))

	// Products — seller-owned writes + owner read-back
	mux.Handle("POST /api/v1/products", seller(http.HandlerFunc(h.createProduct)))
	mux.Handle("GET /api/v1/seller/products", seller(http.HandlerFunc(h.listMyProducts)))
	mux.Handle("GET /api/v1/products/{productId}", seller(http.HandlerFunc(h.getProduct)))
	mux.Handle("PATCH /api/v1/products/{productId}", seller(http.HandlerFunc(h.updateProduct)))
	mux.Handle("DELETE /api/v1/products/{productId}", seller(http.HandlerFunc(h.deleteProduct)))

	// Variants — seller-owned
	mux.Handle("POST /api/v1/products/{productId}/variants", seller(http.HandlerFunc(h.addVariant)))
	mux.Handle("PATCH /api/v1/products/{productId}/variants/{variantId}", seller(http.HandlerFunc(h.updateVariant)))
	mux.Handle("DELETE /api/v1/products/{productId}/variants/{variantId}", seller(http.HandlerFunc(h.deleteVariant)))

	// Admin moderation (RBAC on the owning module — §6)
	mux.Handle("POST /api/v1/products/{productId}/unpublish", admin(http.HandlerFunc(h.unpublishProduct)))
}
```

> The owner read `GET /products/{productId}` sits behind `RequireRole("seller")` so an admin reading it must
> also hold the seller role — acceptable for P6 (admins moderate via unpublish; full public/admin read is P9).
> If you want admins to read here too, drop it to `authn(...)` and rely on the in-handler owner/admin check.

**`cmd/api/main.go`** (updated — adds the catalog module + the SellerSuspended consumer to the P5 version)
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

	catalog "github.com/amoorihesham/eco-api/internal/modules/catalog"
	cataloghandler "github.com/amoorihesham/eco-api/internal/modules/catalog/handler"
	catalogrepo "github.com/amoorihesham/eco-api/internal/modules/catalog/repo"
	catalogservice "github.com/amoorihesham/eco-api/internal/modules/catalog/service"
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

	// --- seller module (P5): repo → service → handler; consumes identity.Reader ---
	sellerSvc := sellerservice.New(pool, sellerrepo.New(pool), identitySvc, outbox)
	sellerH := sellerhandler.New(sellerSvc)

	// --- catalog module (P6): repo → service → handler; consumes seller.Reader for the approved-seller gate ---
	catalogSvc := catalogservice.New(pool, catalogrepo.New(pool), sellerSvc, outbox)
	catalogH := cataloghandler.New(catalogSvc)

	// First cross-module consumer (P5): identity reacts to SellerApproved by promoting the user's role.
	bus.Subscribe(seller.EventSellerApproved, events.Idempotent(pool, "identity",
		func(ctx context.Context, tx pgx.Tx, e events.Event) error {
			var p seller.SellerApprovedPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return err
			}
			return identitySvc.PromoteToSeller(ctx, tx, p.UserID)
		}))

	// Second cross-module consumer (P6): catalog reacts to SellerSuspended by hiding the seller's products.
	// Idempotent + at-least-once; the deactivations + the ProductUnpublished writes commit in one tx.
	bus.Subscribe(seller.EventSellerSuspended, events.Idempotent(pool, "catalog",
		func(ctx context.Context, tx pgx.Tx, e events.Event) error {
			var p seller.SellerSuspendedPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return err
			}
			return catalogSvc.HideSellerProducts(ctx, tx, p.UserID)
		}))

	router := newRouter(logger, healthH, identityH, sellerH, catalogH, jwt)

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
func newRouter(l *slog.Logger, h *health.Handler, identityH *identityhandler.Handler, sellerH *sellerhandler.Handler, catalogH *cataloghandler.Handler, verifier auth.Verifier) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.Live)
	mux.HandleFunc("GET /readyz", h.Ready)

	authn := auth.Authn(verifier)
	identityH.Mount(mux, authn)
	sellerH.Mount(mux, authn)
	catalogH.Mount(mux, authn)

	return httpx.Chain(mux, httpx.RequestID(), httpx.Logger(l), httpx.Recoverer(l))
}
```

> `catalog` is imported only for `seller.EventSellerSuspended`/payload decoding (already from `seller`); the
> `catalog` package import line is kept for symmetry/future reads — drop it if unused to satisfy the compiler.
> `auth.Role(ctx)` is the role accessor the owner read-back uses; if your P3 `auth` exposes it under a
> different name (e.g. `auth.RoleFromContext`), adjust `handler.getProduct` accordingly.

---

## 9. Testing plan

| Test | File | Asserts | Needs DB? |
|---|---|---|---|
| Status→event mapping | `catalog/service/service_test.go` | `emitVisibility` publishes on `draft→active` (Published) and `active→inactive` (Unpublished); `draft→inactive` emits nothing | no |
| Approved-seller gate | `catalog/service/service_test.go` | `CreateProduct` when `seller.Reader` reports `suspended` → `ErrSellerNotApproved` | no |
| Ownership | `catalog/service/service_test.go` | `UpdateProduct` on another seller's product → `ErrNotOwner` | no |
| Variant flips the unit | `catalog/repo/catalog_integration_test.go` | first `AddVariant` sets `has_variants=true`; deleting the last sets it back to `false` | yes |
| Create ±variants + publish | `catalog/repo/catalog_integration_test.go` | create-as-active → product row + **exactly one `ProductPublished`** outbox row; create-as-draft → no event | yes |
| Suspend → hide (consumer) | `catalog/repo/catalog_integration_test.go` | running the `SellerSuspended` consumer flips the seller's active products to `inactive` and writes one `ProductUnpublished` each; **replay is a no-op** | yes |
| Category retire → flag | `catalog/repo/catalog_integration_test.go` | `RetireCategory` sets `retired_at`, hides it from `ListCategories`, and sets `needs_recategorization=true` on its products (none deleted) | yes |

**`internal/modules/catalog/service/service_test.go`** (no Docker — fakes; guard/mapping paths only)
```go
package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/catalog/domain"
	"github.com/amoorihesham/eco-api/internal/modules/catalog/service"
)

// fakeRepo returns canned products; only the read methods used before a tx are exercised here.
type fakeRepo struct {
	product domain.Product
	prodErr error
}

func (f fakeRepo) GetProductByID(context.Context, uuid.UUID) (domain.Product, error) {
	return f.product, f.prodErr
}
func (f fakeRepo) GetCategoryByID(context.Context, uuid.UUID) (domain.Category, error) {
	return domain.Category{ID: f.product.CategoryID}, nil
}

// remaining Repository methods are unused on the guard paths — stub them.
func (fakeRepo) InsertCategory(context.Context, pgx.Tx, domain.Category) error          { return nil }
func (fakeRepo) ListCategories(context.Context) ([]domain.Category, error)              { return nil, nil }
func (fakeRepo) UpdateCategory(context.Context, pgx.Tx, domain.Category) error          { return nil }
func (fakeRepo) RetireCategory(context.Context, pgx.Tx, uuid.UUID) error                { return nil }
func (fakeRepo) FlagProductsForRecategorization(context.Context, pgx.Tx, uuid.UUID) error { return nil }
func (fakeRepo) InsertProduct(context.Context, pgx.Tx, domain.Product) error            { return nil }
func (fakeRepo) ListProductsBySeller(context.Context, uuid.UUID) ([]domain.Product, error) { return nil, nil }
func (fakeRepo) UpdateProduct(context.Context, pgx.Tx, domain.Product) error            { return nil }
func (fakeRepo) UpdateProductStatus(context.Context, pgx.Tx, uuid.UUID, string) error   { return nil }
func (fakeRepo) SetProductHasVariants(context.Context, pgx.Tx, uuid.UUID, bool) error   { return nil }
func (fakeRepo) DeleteProduct(context.Context, pgx.Tx, uuid.UUID) error                 { return nil }
func (fakeRepo) DeactivateActiveProductsBySeller(context.Context, pgx.Tx, uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}
func (fakeRepo) InsertVariant(context.Context, pgx.Tx, domain.Variant) error            { return nil }
func (fakeRepo) GetVariantByID(context.Context, uuid.UUID) (domain.Variant, error)      { return domain.Variant{}, pgx.ErrNoRows }
func (fakeRepo) ListVariantsByProduct(context.Context, uuid.UUID) ([]domain.Variant, error) { return nil, nil }
func (fakeRepo) UpdateVariant(context.Context, pgx.Tx, domain.Variant) error            { return nil }
func (fakeRepo) DeleteVariant(context.Context, pgx.Tx, uuid.UUID) error                 { return nil }
func (fakeRepo) CountVariants(context.Context, uuid.UUID) (int64, error)                { return 0, nil }

// fakeSeller stands in for seller.Reader.
type fakeSeller struct {
	status string
	err    error
}

func (f fakeSeller) SellerStatus(context.Context, uuid.UUID) (domainStatus, error) {
	return domainStatus(f.status), f.err
}

// domainStatus matches seller.Status (a string alias) without importing seller in the test.
type domainStatus = string // NOTE: at type-in time, use seller.Status; see note below.

func TestCreateProductRequiresApprovedSeller(t *testing.T) {
	svc := service.New(nil, fakeRepo{}, fakeSeller{status: "suspended"}, nil)
	_, err := svc.CreateProduct(context.Background(), uuid.New(), service.ProductInput{
		CategoryID: uuid.New(), Title: "T", Currency: "EGP",
	})
	if !errors.Is(err, domain.ErrSellerNotApproved) {
		t.Fatalf("want ErrSellerNotApproved, got %v", err)
	}
}

func TestUpdateProductRejectsNonOwner(t *testing.T) {
	owner, caller := uuid.New(), uuid.New()
	repo := fakeRepo{product: domain.Product{ID: uuid.New(), SellerID: owner, Status: domain.StatusDraft}}
	svc := service.New(nil, repo, fakeSeller{status: "approved"}, nil)
	_, err := svc.UpdateProduct(context.Background(), caller, repo.product.ID, service.ProductInput{
		CategoryID: repo.product.CategoryID, Title: "T", Currency: "EGP",
	})
	if !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("want ErrNotOwner, got %v", err)
	}
}
```

> **Type-in note for the unit test:** `seller.Reader.SellerStatus` returns `seller.Status` (a string alias).
> The skeleton above uses a local `domainStatus = string` alias only so the doc compiles in isolation. When
> you type the real file, import `seller "github.com/amoorihesham/eco-api/internal/modules/seller"` and make
> `fakeSeller.SellerStatus` return `seller.Status`; delete the `domainStatus` shim. The status→event mapping
> assertion (`emitVisibility`) is exercised through `CreateProduct`/`UnpublishProduct` in the integration test
> where a real `outbox` + tx exist; the pure-unit guards above cover the gate and ownership branches that
> return before any transaction (so `pool`/`outbox` may be nil).

**`internal/modules/catalog/repo/catalog_integration_test.go`** (build-tagged; against compose Postgres)
```go
//go:build integration

package repo_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amoorihesham/eco-api/internal/modules/catalog/domain"
	catalogrepo "github.com/amoorihesham/eco-api/internal/modules/catalog/repo"
	catalogservice "github.com/amoorihesham/eco-api/internal/modules/catalog/service"
	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

// stubSeller reports a fixed status (the seller module is exercised in its own P5 suite).
type stubSeller struct{ status string }

func (s stubSeller) SellerStatus(context.Context, uuid.UUID) (sellerStatus, error) {
	return sellerStatus(s.status), nil
}

// sellerStatus matches seller.Status (string alias); at type-in time import seller and use seller.Status.
type sellerStatus = string

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

func TestCatalogLifecycle(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `TRUNCATE catalog_variants, catalog_products, catalog_categories`)
	_, _ = pool.Exec(ctx, `TRUNCATE platform_outbox`)
	_, _ = pool.Exec(ctx, `TRUNCATE platform_processed_events`)

	svc := catalogservice.New(pool, catalogrepo.New(pool), stubSeller{status: "approved"}, events.NewOutbox(pool))
	sellerID := uuid.New()

	// Admin creates a category.
	cat, err := svc.CreateCategory(ctx, catalogservice.CategoryInput{Name: "Shoes"})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	// Create-as-active variant-less product → exactly one ProductPublished.
	p, err := svc.CreateProduct(ctx, sellerID, catalogservice.ProductInput{
		CategoryID: cat.ID, Title: "Plain Tee", Currency: "EGP", BasePriceMinor: 9900, Status: domain.StatusActive, Stock: 5,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	if got := outboxCount(t, pool, ctx, "ProductPublished"); got != 1 {
		t.Fatalf("want 1 ProductPublished, got %d", got)
	}

	// Add a variant → has_variants flips true.
	color := "red"
	if _, err := svc.AddVariant(ctx, sellerID, p.ID, catalogservice.VariantInput{Color: &color, SKU: "TEE-RED", PriceMinor: 9900, Stock: 3}); err != nil {
		t.Fatalf("add variant: %v", err)
	}
	view, _ := svc.GetProduct(ctx, p.ID)
	if !view.Product.HasVariants || len(view.Variants) != 1 {
		t.Fatalf("want has_variants + 1 variant, got %+v", view.Product)
	}

	// Suspend the seller via the consumer → product goes inactive + one ProductUnpublished; replay is a no-op.
	hide := events.Idempotent(pool, "catalog", func(ctx context.Context, tx pgx.Tx, e events.Event) error {
		var pl domain.SellerSuspendedPayloadShim
		if err := json.Unmarshal(e.Payload, &pl); err != nil {
			return err
		}
		return svc.HideSellerProducts(ctx, tx, pl.UserID)
	})
	evt := makeSuspendEvent(t, sellerID)
	if err := hide(ctx, evt); err != nil {
		t.Fatalf("hide 1: %v", err)
	}
	if err := hide(ctx, evt); err != nil { // replay → no-op (event already processed)
		t.Fatalf("hide 2 (replay): %v", err)
	}
	if got := outboxCount(t, pool, ctx, "ProductUnpublished"); got != 1 {
		t.Fatalf("want 1 ProductUnpublished, got %d", got)
	}
	view, _ = svc.GetProduct(ctx, p.ID)
	if view.Product.Status != domain.StatusInactive {
		t.Fatalf("want inactive after suspend, got %q", view.Product.Status)
	}

	// Retire the category → product flagged, never deleted; category hidden from the list.
	if err := svc.RetireCategory(ctx, cat.ID); err != nil {
		t.Fatalf("retire: %v", err)
	}
	view, _ = svc.GetProduct(ctx, p.ID)
	if !view.Product.NeedsRecategorization {
		t.Fatalf("want needs_recategorization=true after retire")
	}
	cats, _ := svc.ListCategories(ctx)
	if len(cats) != 0 {
		t.Fatalf("want retired category hidden, got %d", len(cats))
	}
}

func outboxCount(t *testing.T, pool *pgxpool.Pool, ctx context.Context, typ string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform_outbox WHERE event_type = $1`, typ).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", typ, err)
	}
	return n
}
```

> **Type-in notes for the integration test.** (1) Replace the `sellerStatus` shim with the real
> `seller.Status` (import the seller port). (2) `SellerSuspendedPayloadShim` stands in for
> `seller.SellerSuspendedPayload{UserID, ApplicationID}` — at type-in, decode into `seller.SellerSuspendedPayload`
> and drop the shim. (3) `makeSuspendEvent` builds an `events.Event` whose payload is a
> `seller.SellerSuspendedPayload{UserID: sellerID}` (use `events.NewEvent(seller.EventSellerSuspended, …)` then
> insert it into `platform_outbox`, or construct the `events.Event` directly as P5's `loadOutboxEvent` does).
> (4) Adjust `platform_outbox` / `platform_processed_events` column + table names to the actual P2 names if they
> differ (same caveat as P5 §9).

Run: `task test` (unit) and `task test:integration` (DB-backed).

---

## 10. Definition of Done

- [ ] `task migrate:up` applies cleanly; `task migrate:version` → `6` (not dirty); `catalog_categories`,
      `catalog_products`, `catalog_variants` exist with `uq_catalog_categories_slug` and
      `uq_catalog_variants_product_sku`.
- [ ] `task sqlc` emits `internal/modules/catalog/repo/catalogdb/*`; `task sqlc:check` reports no diff across
      all five gen dirs.
- [ ] `task run` boots; an **admin** `POST /api/v1/categories` → `201`; `GET /api/v1/categories` → `200` lists it;
      a non-admin caller → `403`.
- [ ] An **approved seller** `POST /api/v1/products` → `201` (variant-less, with `stock`); a second product
      then `POST .../variants` → `201` and the product reports `has_variants: true`, `stock: null`.
- [ ] A **suspended** seller's product create → `403` (`ErrSellerNotApproved`); a seller editing another
      seller's product → `403` (`ErrNotOwner`).
- [ ] **Publish event:** activating a product (create-as-active or `draft→active`) publishes exactly one
      `ProductPublished`; deactivating / deleting an active product / admin unpublish publishes one
      `ProductUnpublished`.
- [ ] **Consume `SellerSuspended`:** after an admin suspends a seller (P5) + dispatch, that seller's active
      products become `inactive` and one `ProductUnpublished` is emitted per product; the consumer runs
      **exactly once** per event (replay is a no-op).
- [ ] **Retire safety:** `DELETE /api/v1/categories/{id}` returns `204`, removes it from `GET /categories`, and
      sets `needs_recategorization=true` on its products — **no product is deleted**.
- [ ] `task test` (unit) green — status→event mapping, approved-gate, ownership.
- [ ] `task test:integration` green — create ±variants + publish, suspend→hide + dedupe, retire→flag.
- [ ] `task ci` green (tidy → sqlc generate → lint → test → build).
- [ ] `catalog/domain` + `catalog/service` import no driver/SDK internals; the **seller package gains no
      `catalog` import**; new tables hold the `catalog_` prefix; **no cross-module FK**
      (`catalog_variants.product_id` in-module FK is fine); money in minor units; no new env vars.

*Demo: an admin creates a "Shoes" category; an approved seller creates a product without variants and another
with a red/size-M variant, then activates it (ProductPublished); the admin suspends the seller and the
products go dark (ProductUnpublished); retiring the category flags — but never deletes — the products.*

---

## 11. Verification (PowerShell)

```powershell
# 1. Migrate + generate
task db:up
task migrate:up
task migrate:version          # -> 6
task sqlc

# 2. Build pipeline
task ci                       # tidy, sqlc generate, lint, test, build -> green

# 3. Run, then prepare an admin and an APPROVED seller (second terminal, after `task run`)
#    (a) register an admin, promote, log in
Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/register -ContentType application/json `
  -Body (@{ email = "admin@example.com"; password = "password123"; name = "Admin" } | ConvertTo-Json) | Out-Null
docker compose exec postgres psql -U eco -d eco -c "UPDATE identity_users SET role='admin' WHERE email='admin@example.com';"
$admin = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/login -ContentType application/json `
  -Body (@{ email = "admin@example.com"; password = "password123" } | ConvertTo-Json)
$ah = @{ Authorization = "Bearer $($admin.tokens.access_token)" }

#    (b) register a buyer, apply + admin-approve (P5), then re-login to get a seller JWT
$seller = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/register -ContentType application/json `
  -Body (@{ email = "seller@example.com"; password = "password123"; name = "Seller" } | ConvertTo-Json)
$sh0 = @{ Authorization = "Bearer $($seller.tokens.access_token)" }
$app = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/seller/applications -Headers $sh0 -ContentType application/json `
  -Body (@{ store_name = "Acme"; contact = "acme@x.com" } | ConvertTo-Json)
Invoke-RestMethod -Method Post -Uri "http://localhost:8080/api/v1/admin/sellers/$($app.id)/approve" -Headers $ah | Out-Null
Start-Sleep -Seconds 3   # let the SellerApproved consumer flip the role
$seller2 = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/auth/login -ContentType application/json `
  -Body (@{ email = "seller@example.com"; password = "password123" } | ConvertTo-Json)
$sh = @{ Authorization = "Bearer $($seller2.tokens.access_token)" }
$seller2.user.role            # -> seller

# 4. Admin creates a category; anyone can list it
$cat = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/categories -Headers $ah -ContentType application/json `
  -Body (@{ name = "Shoes" } | ConvertTo-Json)
Invoke-RestMethod -Method Get -Uri http://localhost:8080/api/v1/categories   # -> data: [ { name: "Shoes", slug: "shoes" } ]

# 5. Seller creates a product WITHOUT variants, active -> ProductPublished
$p = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/products -Headers $sh -ContentType application/json `
  -Body (@{ category_id = $cat.id; title = "Runner"; currency = "EGP"; base_price_minor = 49900; status = "active"; stock = 10 } | ConvertTo-Json)
$p.status                     # -> active

# 6. Seller creates a product WITH a variant
$p2 = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/products -Headers $sh -ContentType application/json `
  -Body (@{ category_id = $cat.id; title = "Tee"; currency = "EGP"; base_price_minor = 9900; status = "draft" } | ConvertTo-Json)
Invoke-RestMethod -Method Post -Uri "http://localhost:8080/api/v1/products/$($p2.id)/variants" -Headers $sh -ContentType application/json `
  -Body (@{ color = "red"; size = "M"; sku = "TEE-RED-M"; price_minor = 9900; stock = 7 } | ConvertTo-Json)
(Invoke-RestMethod -Method Get -Uri "http://localhost:8080/api/v1/products/$($p2.id)" -Headers $sh).has_variants   # -> True

# 7. Admin suspends the seller -> after dispatch, the active product is hidden (status inactive)
Invoke-RestMethod -Method Post -Uri "http://localhost:8080/api/v1/admin/sellers/$($app.id)/suspend" -Headers $ah | Out-Null
Start-Sleep -Seconds 3
(Invoke-RestMethod -Method Get -Uri "http://localhost:8080/api/v1/products/$($p.id)" -Headers $sh).status   # -> inactive

# 8. A suspended seller can no longer create products (403)
try {
  Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/products -Headers $sh -ContentType application/json `
    -Body (@{ category_id = $cat.id; title = "Nope"; currency = "EGP"; base_price_minor = 100 } | ConvertTo-Json)
} catch { $_.Exception.Response.StatusCode.value__ }   # -> 403

# 9. Retire the category -> 204; it disappears from the list; products are flagged, not deleted
Invoke-RestMethod -Method Delete -Uri "http://localhost:8080/api/v1/categories/$($cat.id)" -Headers $ah
Invoke-RestMethod -Method Get -Uri http://localhost:8080/api/v1/categories   # -> data: []

# 10. The atomic-publish + cross-module guarantees, end to end
task test:integration

# 11. Tables exist with the catalog_ prefix
docker compose exec postgres psql -U eco -d eco -c "\dt catalog_*"
```

---

## 12. Handoff to P7 (Product Media) — and P8/P9/P10

P6 leaves clean seams the next phases plug into with no rework:

- **P7 (Media):** the `catalog_products` row + the `Product` aggregate are the attach point. P7 adds a storage
  port + a `catalog_product_images` table (ordering + URL) and exposes image URLs on the product DTO
  (`images: []`), via `POST /api/v1/products/{id}/images`. No change to the visibility/event model here.
- **P8 (Inventory):** `stock` per **sellable unit** (variant or variant-less product) lives here; P8 builds the
  authoritative availability query + the decrement-on-payment rule **over** these numbers — P6 never decrements.
- **P9 (Discovery):** the **public read side** consumes the visibility rule (`status=active` + seller
  `approved`) and the `catalog.Reader`/data established here — listing with filters/sort, keyword search, and the
  rich public product detail (with seller info + availability). P6's owner-only reads stay; P9 adds the public ones.
- **P10 (Cart):** consumes **`ProductUnpublished`** (`bus.Subscribe(catalog.EventProductUnpublished, …)`) to
  prune stale lines, and reads price/stock via **`catalog.Reader.GetSellable`** — the port this phase exposes.
- **The new-module + consumer template is now proven three times** (identity, seller, catalog): a sixth migration,
  a fifth sqlc target, and the `events.Idempotent` consumer shape are the reusable mold for every later module.
```
