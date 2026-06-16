package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Event is the immutable envelope every domain event travels in.
// ID is the idempotency key: stable from creation, through the outbox and bus, to each consumer.
type Event struct {
	ID         uuid.UUID       // unique per event
	Type       string          // e.g. "UserRegistered"
	OccurredAt time.Time       // when the producing state change happened (UTC)
	Payload    json.RawMessage // module-defined, JSON-encoded
}

// Handler reacts to one event. Handlers MUST be idempotent — delivery is at-least-once.
type Handler func(ctx context.Context, e Event) error

// Publisher relays a committed event to subscribers. Implemented by the bus; called by the dispatcher.
type Publisher interface {
	Publish(ctx context.Context, e Event) error
}

// Subscriber registers handlers by event type. Modules call this at wiring time.
type Subscriber interface {
	Subscribe(eventType string, h Handler)
}

// NewEvent builds an envelope: a fresh ID, the current UTC time, and the JSON-encoded payload.
func NewEvent(eventType string, payload any) (Event, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("marshal payload for %s: %w", eventType, err)
	}
	return Event{
		ID:         uuid.New(),
		Type:       eventType,
		OccurredAt: time.Now().UTC(),
		Payload:    b,
	}, nil
}
