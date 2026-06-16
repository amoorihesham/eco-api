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
