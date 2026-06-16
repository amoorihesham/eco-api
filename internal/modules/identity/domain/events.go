package domain

import "github.com/google/uuid"

// EventUserRegistered is published (atomically, via the outbox) when a buyer registers.
// Consumed by notification (P16) for the welcome email.
const EventUserRegistered = "UserRegistered"

// UserRegisteredPayload is the JSON payload carried by EventUserRegistered.
type UserRegisteredPayload struct {
	UserID uuid.UUID `json:"user_id"`
	Email  string    `json:"email"`
}
