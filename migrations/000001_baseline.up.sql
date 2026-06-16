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
