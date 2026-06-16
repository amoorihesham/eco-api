package handler

import (
	"net/http"

	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/httpx"
)

// Mount registers the seller routes under /api/v1. apply/getMyApplication need only authentication (a buyer
// applies); the store requires the seller role; the admin lifecycle requires the admin role. The caller id
// always comes from the verified token (auth.UserID), never the body/path.
func (h *Handler) Mount(mux *http.ServeMux, authn httpx.Middleware) {
	seller := func(next http.Handler) http.Handler { return authn(auth.RequireRole("seller")(next)) }
	admin := func(next http.Handler) http.Handler { return authn(auth.RequireRole("admin")(next)) }

	// Seller self-service
	mux.Handle("POST /api/v1/seller/applications", authn(http.HandlerFunc(h.apply)))
	mux.Handle("GET /api/v1/seller/application", authn(http.HandlerFunc(h.getMyApplication)))
	mux.Handle("GET /api/v1/seller/store", seller(http.HandlerFunc(h.getMyStore)))
	mux.Handle("PATCH /api/v1/seller/store", seller(http.HandlerFunc(h.updateMyStore)))

	// Admin lifecycle (RBAC-gated operations on the owning module — P5 §6)
	mux.Handle("POST /api/v1/admin/sellers/{sellerId}/approve", admin(http.HandlerFunc(h.approve)))
	mux.Handle("POST /api/v1/admin/sellers/{sellerId}/reject", admin(http.HandlerFunc(h.reject)))
	mux.Handle("POST /api/v1/admin/sellers/{sellerId}/suspend", admin(http.HandlerFunc(h.suspend)))
}
