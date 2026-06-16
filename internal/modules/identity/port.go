// Package identity is the public port surface of the identity module:
// the published event type and the Reader port other modules consume.
package identity

import (
	"context"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
)

// EventUserRegistered is this module's published event (the single public surface for producers).
const EventUserRegistered = domain.EventUserRegistered

// Reader is the read port sibling modules (P4 account, P5 seller) consume to resolve a user by ID.
// They import ONLY this file — never service/repo/domain. *service.Service satisfies it (P4 wires it).
type Reader interface {
	UserByID(ctx context.Context, id uuid.UUID) (PublicUser, error)
}

// PublicUser is the cross-module projection (no password hash crosses the boundary).
type PublicUser struct {
	ID    uuid.UUID
	Email string
	Name  string
	Role  string
}
