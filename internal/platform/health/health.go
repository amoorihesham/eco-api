package health

import (
	"context"
	"net/http"

	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// Check is a named readiness probe; Func returns nil when healthy.
type Check struct {
	Name string
	Func func(ctx context.Context) error
}

// Handler serves liveness and readiness endpoints over a set of checks.
type Handler struct {
	checks []Check
}

// New builds a health handler. P0 registers no checks; P1 adds the DB check.
func New(checks ...Check) *Handler {
	return &Handler{checks: checks}
}

type statusResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Live reports that the process is up.
func (h *Handler) Live(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

// Ready runs all checks; any failure yields 503.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	results := make(map[string]string, len(h.checks))
	healthy := true
	for _, c := range h.checks {
		if err := c.Func(r.Context()); err != nil {
			results[c.Name] = err.Error()
			healthy = false
			continue
		}
		results[c.Name] = "ok"
	}
	if !healthy {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, statusResponse{Status: "unavailable", Checks: results})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, statusResponse{Status: "ok", Checks: results})
}
