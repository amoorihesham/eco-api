// Package domain holds the seller module's pure entities, value objects, and lifecycle rules.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Status is the seller lifecycle state (PRD FR-5/FR-6/FR-8; mirrors OpenAPI SellerStatus).
type Status string

// The seller lifecycle states.
const (
	StatusPending   Status = "pending"
	StatusApproved  Status = "approved"
	StatusRejected  Status = "rejected"
	StatusSuspended Status = "suspended"
)

// RoleSeller is the identity role value a user holds once approved (compared against identity.PublicUser.Role
// without importing identity/domain).
const RoleSeller = "seller"

// Application is a buyer's request to become a seller. reject_reason/decided_at are server-owned and not
// part of the public DTO.
type Application struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	Status       Status
	StoreName    string
	Description  string
	Contact      string
	RejectReason string
	CreatedAt    time.Time
}

// Store is an approved seller's public storefront profile.
type Store struct {
	ID          uuid.UUID
	SellerID    uuid.UUID // = identity user id
	Name        string
	LogoURL     string
	Description string
	Contact     string
}

// CanApprove reports whether a is eligible to be approved (an admin action is legal only from a specific
// source status; illegal transitions return 409).
func (a Application) CanApprove() bool { return a.Status == StatusPending }

// CanReject reports whether a is eligible to be rejected.
func (a Application) CanReject() bool { return a.Status == StatusPending }

// CanSuspend reports whether a is eligible to be suspended.
func (a Application) CanSuspend() bool { return a.Status == StatusApproved }
