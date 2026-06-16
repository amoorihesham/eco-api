package events_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/amoorihesham/eco-api/internal/platform/events"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestBusFansOutToAllSubscribers(t *testing.T) {
	bus := events.NewBus(quietLogger())
	var a, b int
	bus.Subscribe("Thing", func(context.Context, events.Event) error { a++; return nil })
	bus.Subscribe("Thing", func(context.Context, events.Event) error { b++; return nil })

	e, err := events.NewEvent("Thing", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	if err := bus.Publish(context.Background(), e); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if a != 1 || b != 1 {
		t.Fatalf("want both handlers called once, got a=%d b=%d", a, b)
	}
}

func TestBusPropagatesHandlerError(t *testing.T) {
	bus := events.NewBus(quietLogger())
	want := errors.New("boom")
	bus.Subscribe("Thing", func(context.Context, events.Event) error { return want })

	e, _ := events.NewEvent("Thing", nil)
	if err := bus.Publish(context.Background(), e); !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
}
