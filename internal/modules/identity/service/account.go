package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/platform/db"
)

// AddressInput carries the editable fields of an address (id/user/created_at are server-owned).
type AddressInput struct {
	Recipient  string
	Line1      string
	Line2      string
	City       string
	Region     string
	PostalCode string
	Country    string
	Phone      string
	IsDefault  bool
}

// GetProfile returns the current user. Maps a missing row to ErrUserNotFound.
func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (domain.User, error) {
	u, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, domain.ErrUserNotFound
		}
		return domain.User{}, err
	}
	return u, nil
}

// UpdateProfile updates the user's display name and returns the fresh row.
func (s *Service) UpdateProfile(ctx context.Context, userID uuid.UUID, name string) (domain.User, error) {
	var u domain.User
	err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		u, err = s.repo.UpdateUserName(ctx, tx, userID, name)
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, domain.ErrUserNotFound
		}
		return domain.User{}, err
	}
	return u, nil
}

// ListAddresses returns the caller's addresses, default first.
func (s *Service) ListAddresses(ctx context.Context, userID uuid.UUID) ([]domain.Address, error) {
	return s.repo.ListAddresses(ctx, userID)
}

// GetAddress returns one owner-scoped address; a missing-or-foreign id yields ErrAddressNotFound.
func (s *Service) GetAddress(ctx context.Context, userID, addressID uuid.UUID) (domain.Address, error) {
	a, err := s.repo.GetAddress(ctx, userID, addressID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Address{}, domain.ErrAddressNotFound
		}
		return domain.Address{}, err
	}
	return a, nil
}

// CreateAddress adds an address. The first address is forced default; a requested default demotes the
// rest. Clear-then-insert run in one tx so the partial-unique index is never tripped.
func (s *Service) CreateAddress(ctx context.Context, userID uuid.UUID, in AddressInput) (domain.Address, error) {
	count, err := s.repo.CountAddresses(ctx, userID)
	if err != nil {
		return domain.Address{}, err
	}
	makeDefault := wantDefault(count, in.IsDefault)

	a := domain.Address{
		ID:         uuid.New(),
		UserID:     userID,
		Recipient:  in.Recipient,
		Line1:      in.Line1,
		Line2:      in.Line2,
		City:       in.City,
		Region:     in.Region,
		PostalCode: in.PostalCode,
		Country:    in.Country,
		Phone:      in.Phone,
		IsDefault:  makeDefault,
		CreatedAt:  time.Now().UTC(),
	}
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if makeDefault {
			if err := s.repo.ClearDefaultAddresses(ctx, tx, userID); err != nil {
				return err
			}
		}
		return s.repo.InsertAddress(ctx, tx, a)
	})
	if err != nil {
		return domain.Address{}, err
	}
	return a, nil
}

// UpdateAddress edits an owner-scoped address. Promoting to default demotes the rest; an address that
// is already default stays default (you move the default by promoting another, never by clearing it).
func (s *Service) UpdateAddress(ctx context.Context, userID, addressID uuid.UUID, in AddressInput) (domain.Address, error) {
	existing, err := s.GetAddress(ctx, userID, addressID) // 404 if missing/foreign
	if err != nil {
		return domain.Address{}, err
	}
	makeDefault := existing.IsDefault || in.IsDefault

	updated := domain.Address{
		ID:         existing.ID,
		UserID:     userID,
		Recipient:  in.Recipient,
		Line1:      in.Line1,
		Line2:      in.Line2,
		City:       in.City,
		Region:     in.Region,
		PostalCode: in.PostalCode,
		Country:    in.Country,
		Phone:      in.Phone,
		IsDefault:  makeDefault,
		CreatedAt:  existing.CreatedAt,
	}
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if in.IsDefault && !existing.IsDefault {
			if err := s.repo.ClearDefaultAddresses(ctx, tx, userID); err != nil {
				return err
			}
		}
		return s.repo.UpdateAddress(ctx, tx, updated)
	})
	if err != nil {
		return domain.Address{}, err
	}
	return updated, nil
}

// DeleteAddress removes an owner-scoped address. Deleting the default promotes the newest remaining one
// so the invariant (one default while any address exists) holds.
func (s *Service) DeleteAddress(ctx context.Context, userID, addressID uuid.UUID) error {
	existing, err := s.GetAddress(ctx, userID, addressID) // 404 if missing/foreign
	if err != nil {
		return err
	}
	return db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := s.repo.DeleteAddress(ctx, tx, userID, addressID)
		if err != nil {
			return err
		}
		if rows == 0 {
			return domain.ErrAddressNotFound
		}
		if !existing.IsDefault {
			return nil
		}
		newest, err := s.repo.NewestAddressID(ctx, tx, userID) // same tx: sees the delete
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // book is now empty — zero defaults is valid
			}
			return err
		}
		return s.repo.SetAddressDefault(ctx, tx, userID, newest)
	})
}

// wantDefault reports whether a new address should be the default: the first one always is, or one the
// caller explicitly requests.
func wantDefault(existingCount int, requested bool) bool {
	return requested || existingCount == 0
}
