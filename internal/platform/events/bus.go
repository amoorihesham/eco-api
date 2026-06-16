package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Bus is the in-process event bus: synchronous fan-out to handlers subscribed by event type.
// It satisfies both Publisher (used by the dispatcher) and Subscriber (used by modules at wiring).
type Bus struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
	log      *slog.Logger
}

func NewBus(log *slog.Logger) *Bus {
	return &Bus{handlers: make(map[string][]Handler), log: log}
}

// Subscribe registers a handler for an event type. Call during wiring, before Run.
func (b *Bus) Subscribe(eventType string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

// Publish delivers e to every handler for its type, in registration order.
// A handler error aborts the publish so the dispatcher leaves the outbox row undispatched
// and retries on the next tick — which is safe because handlers are idempotent.
func (b *Bus) Publish(ctx context.Context, e Event) error {
	b.mu.RLock()
	hs := b.handlers[e.Type]
	b.mu.RUnlock()

	for _, h := range hs {
		if err := h(ctx, e); err != nil {
			return fmt.Errorf("handle %s (%s): %w", e.Type, e.ID, err)
		}
	}
	return nil
}
