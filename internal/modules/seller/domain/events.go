package domain

import "github.com/google/uuid"

// Events published (atomically, via the outbox) on the seller lifecycle.
// SellerApproved is consumed by identity (role flip, P5) + catalog/notification (P6/P16);
// SellerSuspended is consumed by catalog (hide products, P6) + notification (P16).
const (
	EventSellerApproved  = "SellerApproved"
	EventSellerSuspended = "SellerSuspended"
)

// SellerApprovedPayload is the SellerApproved event body.
type SellerApprovedPayload struct {
	UserID        uuid.UUID `json:"user_id"`
	ApplicationID uuid.UUID `json:"application_id"`
}

// SellerSuspendedPayload is the SellerSuspended event body.
type SellerSuspendedPayload struct {
	UserID        uuid.UUID `json:"user_id"`
	ApplicationID uuid.UUID `json:"application_id"`
}
