// Package domain holds the identity module's aggregates, value objects,
// and domain errors, independent of any storage or transport concern.
package domain

import "errors"

// Sentinel errors returned by the identity service for known failure cases.
var (
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid or expired token")

	// P4 — Account.
	ErrUserNotFound    = errors.New("user not found")
	ErrAddressNotFound = errors.New("address not found") // also returned for a foreign address (no leak)
)
