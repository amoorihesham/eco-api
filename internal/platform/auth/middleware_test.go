package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/platform/auth"
)

func TestRBAC(t *testing.T) {
	j := auth.NewJWT("test-secret-at-least-32-bytes-long!!", time.Hour)
	admin, _, _ := j.Issue(uuid.New(), "admin")
	buyer, _, _ := j.Issue(uuid.New(), "buyer")

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	protected := auth.Authn(j)(auth.RequireRole("admin")(ok))

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"bad token", "Bearer nope", http.StatusUnauthorized},
		{"wrong role", "Bearer " + buyer, http.StatusForbidden},
		{"right role", "Bearer " + admin, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ping", nil)
			if c.header != "" {
				r.Header.Set("Authorization", c.header)
			}
			w := httptest.NewRecorder()
			protected.ServeHTTP(w, r)
			if w.Code != c.want {
				t.Fatalf("want %d, got %d", c.want, w.Code)
			}
		})
	}
}
