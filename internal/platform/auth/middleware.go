package auth

import (
	"net/http"
	"strings"

	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// Authn verifies the bearer access token and stores the claims in the request context.
func Authn(v Verifier) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearer(r)
			if raw == "" {
				httpx.Unauthorized(w, "missing bearer token")
				return
			}
			claims, err := v.Verify(raw)
			if err != nil {
				httpx.Unauthorized(w, "invalid or expired token")
				return
			}
			next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
		})
	}
}

// RequireRole rejects a request whose verified role is not in the allow-list. Use after Authn.
func RequireRole(roles ...string) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := ClaimsFrom(r.Context())
			if !ok {
				httpx.Unauthorized(w, "authentication required")
				return
			}
			for _, want := range roles {
				if c.Role == want {
					next.ServeHTTP(w, r)
					return
				}
			}
			httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "insufficient role")
		})
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
