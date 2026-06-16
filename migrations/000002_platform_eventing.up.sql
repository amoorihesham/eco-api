-- P2 Eventing Foundation: transactional-outbox infrastructure (platform_ prefix → not business tables).

-- Events awaiting dispatch. Written in the SAME tx as the producing state change (atomic publish).
CREATE TABLE platform_outbox (
    id            uuid        PRIMARY KEY,
    event_type    text        NOT NULL,
    payload       jsonb       NOT NULL,
    occurred_at   timestamptz NOT NULL,
    dispatched_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Partial index: the dispatcher only ever scans undispatched rows, in arrival order.
CREATE INDEX idx_platform_outbox_undispatched
    ON platform_outbox (created_at)
    WHERE dispatched_at IS NULL;

-- Per-consumer dedupe ledger: makes at-least-once delivery safe (idempotent handling).
CREATE TABLE platform_processed_events (
    consumer     text        NOT NULL,
    event_id     uuid        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);
