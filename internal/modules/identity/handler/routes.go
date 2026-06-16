package handler

import (
	"net/http"

	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// Mount registers the identity routes under /api/v1. Auth endpoints are public except logout; every
// Account (/me*) route requires a valid access token, so it is wrapped with the Authn middleware
// supplied by the composition root.
func (h *Handler) Mount(mux *http.ServeMux, authn httpx.Middleware) {
	// Authentication (P3)
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.refresh)
	mux.Handle("POST /api/v1/auth/logout", authn(http.HandlerFunc(h.logout)))
	mux.HandleFunc("POST /api/v1/auth/password/forgot", h.forgotPassword)
	mux.HandleFunc("POST /api/v1/auth/password/reset", h.resetPassword)

	// Account (P4) — all behind Authn (the caller id comes from the verified token).
	mux.Handle("GET /api/v1/me", authn(http.HandlerFunc(h.getMe)))
	mux.Handle("PATCH /api/v1/me", authn(http.HandlerFunc(h.updateMe)))
	mux.Handle("GET /api/v1/me/addresses", authn(http.HandlerFunc(h.listAddresses)))
	mux.Handle("POST /api/v1/me/addresses", authn(http.HandlerFunc(h.createAddress)))
	mux.Handle("GET /api/v1/me/addresses/{addressId}", authn(http.HandlerFunc(h.getAddress)))
	mux.Handle("PATCH /api/v1/me/addresses/{addressId}", authn(http.HandlerFunc(h.updateAddress)))
	mux.Handle("DELETE /api/v1/me/addresses/{addressId}", authn(http.HandlerFunc(h.deleteAddress)))
}
