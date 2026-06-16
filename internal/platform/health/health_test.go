package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiveOK(t *testing.T) {
	rr := httptest.NewRecorder()
	New().Live(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
}

func TestReadyFailsWhenCheckFails(t *testing.T) {
	h := New(Check{Name: "dep", Func: func(context.Context) error { return errors.New("down") }})
	rr := httptest.NewRecorder()
	h.Ready(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rr.Code)
	}
}
