package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrForbidden is returned by EnsureOwner when the caller does not own a shared resource.
// Map it to httpx 403 in handlers (the 403 case — distinct from tenant-scoped 404; see P4 §6).
var ErrForbidden = errors.New("forbidden")

// UserID returns the authenticated caller's id from the request context (placed by Authn).
// ok is false when the request was not authenticated.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	c, ok := ClaimsFrom(ctx)
	if !ok {
		return uuid.Nil, false
	}
	return c.UserID, true
}

// EnsureOwner enforces that the caller owns a shared, fetched resource. Use this for resources that
// are addressable across users (a seller's product/store, P5+). For collections that are entirely
// private to the caller (the address book), prefer owner-scoped queries that return 404 on a miss.
func EnsureOwner(callerID, ownerID uuid.UUID) error {
	if callerID != ownerID {
		return ErrForbidden
	}
	return nil
}
