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
