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
