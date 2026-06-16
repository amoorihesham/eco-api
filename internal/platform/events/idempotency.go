package events

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/events/eventsdb"
)

// TxHandler is an effect that runs inside the dedupe transaction, on the same tx as the
// processed-events mark — so "did the work" and "marked processed" commit atomically.
type TxHandler func(ctx context.Context, tx pgx.Tx, e Event) error

// Idempotent adapts a TxHandler into a bus Handler that runs at most once per (consumer, event_id).
// MarkProcessed returns rows-affected: 1 = first time (run the effect), 0 = duplicate (no-op).
func Idempotent(pool *pgxpool.Pool, consumer string, h TxHandler) Handler {
	q := eventsdb.New(pool)
	return func(ctx context.Context, e Event) error {
		return db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
			n, err := q.WithTx(tx).MarkProcessed(ctx, eventsdb.MarkProcessedParams{
				Consumer: consumer,
				EventID:  e.ID,
			})
			if err != nil {
				return fmt.Errorf("mark processed: %w", err)
			}
			if n == 0 {
				return nil // already handled by this consumer → skip
			}
			return h(ctx, tx, e)
		})
	}
}
