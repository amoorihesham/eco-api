// Package service implements the identity module's use cases (register,
// login, refresh, logout, password reset) against its ports.
package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

// Repository is the persistence port the service needs; repo/ implements it over sqlc.
// Write methods take pgx.Tx so the service composes them with the outbox in one RunInTx.
// Read methods return pgx.ErrNoRows when the row is absent (the service maps that to domain errors).
type Repository interface {
	CreateUser(ctx context.Context, tx pgx.Tx, u domain.User) error
	GetUserByEmail(ctx context.Context, email string) (domain.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	UpdatePasswordHash(ctx context.Context, tx pgx.Tx, userID uuid.UUID, hash string) error

	InsertRefreshToken(ctx context.Context, tx pgx.Tx, rt domain.RefreshToken) error
	GetRefreshToken(ctx context.Context, tokenHash string) (domain.RefreshToken, error)
	DeleteRefreshToken(ctx context.Context, tx pgx.Tx, tokenHash string) error
	DeleteUserRefreshTokens(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error

	InsertPasswordReset(ctx context.Context, tx pgx.Tx, pr domain.PasswordReset) error
	GetActivePasswordReset(ctx context.Context, tokenHash string) (domain.PasswordReset, error)
	MarkPasswordResetUsed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
}

// Outbox is the publish port (satisfied by *events.Outbox) — kept narrow for testability.
type Outbox interface {
	Write(ctx context.Context, tx pgx.Tx, e events.Event) error
}
