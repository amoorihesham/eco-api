package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "github.com/amoorihesham/eco-api/internal/modules/identity"
	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
)

// UserByID resolves a user for sibling modules via the identity.Reader port (P5 seller wires it).
// Only the public projection crosses the boundary — never the password hash.
func (s *Service) UserByID(ctx context.Context, id uuid.UUID) (identity.PublicUser, error) {
	u, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return identity.PublicUser{}, domain.ErrUserNotFound
		}
		return identity.PublicUser{}, err
	}
	return identity.PublicUser{ID: u.ID, Email: u.Email, Name: u.Name, Role: string(u.Role)}, nil
}
