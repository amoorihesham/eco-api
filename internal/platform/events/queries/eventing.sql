-- name: InsertOutbox :exec
INSERT INTO platform_outbox (id, event_type, payload, occurred_at)
VALUES ($1, $2, $3, $4);

-- name: FetchUnsentOutbox :many
SELECT id, event_type, payload, occurred_at
FROM platform_outbox
WHERE dispatched_at IS NULL
ORDER BY created_at
LIMIT $1;

-- name: MarkOutboxDispatched :exec
UPDATE platform_outbox
SET dispatched_at = now()
WHERE id = $1;

-- name: MarkProcessed :execrows
INSERT INTO platform_processed_events (consumer, event_id)
VALUES ($1, $2)
ON CONFLICT (consumer, event_id) DO NOTHING;
