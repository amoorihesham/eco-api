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
