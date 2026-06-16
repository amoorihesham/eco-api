// Package seller is the public port surface of the seller module: the events it publishes and the
// Reader port other modules (P6 catalog) consume to look up a seller's status.
package seller

import (
	"context"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
)

// Published events (the single public surface for producers/consumers).
const (
	EventSellerApproved  = domain.EventSellerApproved
	EventSellerSuspended = domain.EventSellerSuspended
)

// Payload and status aliases so consumers (the identity subscriber in main.go, P6 catalog) decode
// without importing seller/domain.
type (
	// ApprovedPayload is the SellerApproved event body.
	ApprovedPayload = domain.SellerApprovedPayload
	// SuspendedPayload is the SellerSuspended event body.
	SuspendedPayload = domain.SellerSuspendedPayload
	// Status is the seller lifecycle state.
	Status = domain.Status
)

// Reader is the read port sibling modules consume to gate on seller status (P6 hides a suspended seller's
// products). They import ONLY this file. *service.Service satisfies it.
type Reader interface {
	SellerStatus(ctx context.Context, userID uuid.UUID) (Status, error)
}
