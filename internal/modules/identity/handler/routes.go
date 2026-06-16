package handler

import (
	"net/http"

	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// Mount registers the Authentication routes under /api/v1. logout requires a valid access token,
// so it is wrapped with the Authn middleware supplied by the composition root.
func (h *Handler) Mount(mux *http.ServeMux, authn httpx.Middleware) {
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.refresh)
	mux.Handle("POST /api/v1/auth/logout", authn(http.HandlerFunc(h.logout)))
	mux.HandleFunc("POST /api/v1/auth/password/forgot", h.forgotPassword)
	mux.HandleFunc("POST /api/v1/auth/password/reset", h.resetPassword)
}
