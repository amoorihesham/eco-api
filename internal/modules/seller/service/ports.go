package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

// Repository is the persistence port the service needs; repo/ implements it over sqlc. Write methods take
// pgx.Tx so the service composes them with the outbox in one RunInTx; read methods take ctx and return
// pgx.ErrNoRows when absent (the service maps that to domain errors).
type Repository interface {
	InsertApplication(ctx context.Context, tx pgx.Tx, a domain.Application) error
	GetApplicationByID(ctx context.Context, id uuid.UUID) (domain.Application, error)
	GetActiveApplicationByUser(ctx context.Context, userID uuid.UUID) (domain.Application, error)
	GetLatestApplicationByUser(ctx context.Context, userID uuid.UUID) (domain.Application, error)
	UpdateApplicationStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status, rejectReason string) error

	InsertStore(ctx context.Context, tx pgx.Tx, s domain.Store) error
	GetStoreBySeller(ctx context.Context, sellerID uuid.UUID) (domain.Store, error)
	UpdateStore(ctx context.Context, tx pgx.Tx, s domain.Store) error
}

// Outbox is the publish port (satisfied by *events.Outbox) — kept narrow for testability.
type Outbox interface {
	Write(ctx context.Context, tx pgx.Tx, e events.Event) error
}
