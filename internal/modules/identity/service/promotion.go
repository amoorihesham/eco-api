package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
)

// PromoteToSeller sets a user's role to seller. It is called by the SellerApproved consumer (wired in
// cmd/api/main.go) inside the events.Idempotent dedupe transaction, so "role flipped" and "event marked
// processed" commit together. Idempotent: re-running on an already-seller user is a no-op UPDATE.
func (s *Service) PromoteToSeller(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	return s.repo.UpdateUserRole(ctx, tx, userID, string(domain.RoleSeller))
}
