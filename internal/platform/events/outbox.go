package events

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amoorihesham/eco-api/internal/platform/events/eventsdb"
)

// Outbox writes events into platform_outbox. Producers call Write on the SAME tx as their state
// change, so the change and the event commit atomically (transactional outbox).
type Outbox struct {
	q *eventsdb.Queries
}

func NewOutbox(pool *pgxpool.Pool) *Outbox {
	return &Outbox{q: eventsdb.New(pool)}
}

// Write enlists the event insert in the caller's transaction.
func (o *Outbox) Write(ctx context.Context, tx pgx.Tx, e Event) error {
	return o.q.WithTx(tx).InsertOutbox(ctx, eventsdb.InsertOutboxParams{
		ID:         e.ID,
		EventType:  e.Type,
		Payload:    e.Payload,
		OccurredAt: e.OccurredAt,
	})
}

// Dispatcher relays committed outbox rows to the bus. One instance per process (MVP); SKIP LOCKED /
// multi-instance dispatch is a deliberate non-goal until the broker phase (ARCHITECTURE §15).
type Dispatcher struct {
	pool     *pgxpool.Pool
	q        *eventsdb.Queries
	bus      Publisher
	log      *slog.Logger
	interval time.Duration
	batch    int32
}

func NewDispatcher(pool *pgxpool.Pool, bus Publisher, log *slog.Logger, interval time.Duration, batch int32) *Dispatcher {
	return &Dispatcher{pool: pool, q: eventsdb.New(pool), bus: bus, log: log, interval: interval, batch: batch}
}

// DrainOnce dispatches one batch of undispatched events. On a publish error it stops and returns —
// the row stays undispatched and is retried next tick.
func (d *Dispatcher) DrainOnce(ctx context.Context) error {
	rows, err := d.q.FetchUnsentOutbox(ctx, d.batch)
	if err != nil {
		return fmt.Errorf("fetch outbox: %w", err)
	}
	for _, r := range rows {
		e := Event{ID: r.ID, Type: r.EventType, OccurredAt: r.OccurredAt, Payload: r.Payload}
		if err := d.bus.Publish(ctx, e); err != nil {
			return fmt.Errorf("publish %s: %w", r.ID, err)
		}
		if err := d.q.MarkOutboxDispatched(ctx, r.ID); err != nil {
			return fmt.Errorf("mark dispatched %s: %w", r.ID, err)
		}
	}
	return nil
}

// Run polls on the configured interval until ctx is cancelled, then does one final best-effort drain
// (graceful shutdown — the P0 lifecycle).
func (d *Dispatcher) Run(ctx context.Context) error {
	t := time.NewTicker(d.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := d.DrainOnce(drainCtx); err != nil {
				d.log.Error("final outbox drain failed", slog.String("error", err.Error()))
			}
			cancel()
			return ctx.Err()
		case <-t.C:
			if err := d.DrainOnce(ctx); err != nil {
				d.log.Error("outbox drain failed", slog.String("error", err.Error()))
			}
		}
	}
}
