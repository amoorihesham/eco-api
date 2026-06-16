package domain

import (
	"time"

	"github.com/google/uuid"
)

// Role is the single role each account carries (PRD FR-4).
type Role string

// The roles an account may carry; each account has exactly one.
const (
	RoleBuyer  Role = "buyer"
	RoleSeller Role = "seller"
	RoleAdmin  Role = "admin"
)

// User is the identity aggregate. PasswordHash never leaves the module boundary.
type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	Name         string
	Role         Role
	CreatedAt    time.Time
}

// RefreshToken / PasswordReset are persisted value objects (the plaintext token is never stored).
type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
}

// PasswordReset is a persisted password-reset request; the plaintext token
// is never stored, only its hash.
type PasswordReset struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
}
