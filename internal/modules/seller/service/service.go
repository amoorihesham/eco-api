// Package service implements the seller module's use cases (apply, admin lifecycle, store profile)
// against its ports.
package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "github.com/amoorihesham/eco-api/internal/modules/identity"
	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

// Service implements the seller use cases. It depends only on ports: its Repository, the Outbox, and the
// identity.Reader (P4) for the one synchronous cross-module read (the "already a seller" guard).
type Service struct {
	pool   db.Beginner
	repo   Repository
	users  identity.Reader
	outbox Outbox
}

// New builds a Service over its ports.
func New(pool db.Beginner, repo Repository, users identity.Reader, outbox Outbox) *Service {
	return &Service{pool: pool, repo: repo, users: users, outbox: outbox}
}

// ApplicationInput carries the editable fields of a seller application (ids/status/timestamps are
// server-owned).
type ApplicationInput struct {
	StoreName   string
	Description string
	Contact     string
}

// StoreInput carries the editable fields of a store profile.
type StoreInput struct {
	Name        string
	LogoURL     string
	Description string
	Contact     string
}

// Apply submits a seller application. Rejects if the caller is already a seller (sync read via
// identity.Reader) or already has an active application (PRD FR-5).
func (s *Service) Apply(ctx context.Context, userID uuid.UUID, in ApplicationInput) (domain.Application, error) {
	u, err := s.users.UserByID(ctx, userID)
	if err != nil {
		return domain.Application{}, err
	}
	if u.Role == domain.RoleSeller {
		return domain.Application{}, domain.ErrAlreadySeller
	}
	if _, err := s.repo.GetActiveApplicationByUser(ctx, userID); err == nil {
		return domain.Application{}, domain.ErrApplicationExists
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Application{}, err
	}

	a := domain.Application{
		ID:          uuid.New(),
		UserID:      userID,
		Status:      domain.StatusPending,
		StoreName:   in.StoreName,
		Description: in.Description,
		Contact:     in.Contact,
		CreatedAt:   time.Now().UTC(),
	}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.InsertApplication(ctx, tx, a)
	}); err != nil {
		return domain.Application{}, err
	}
	return a, nil
}

// GetMyApplication returns the caller's latest application (any status); none → ErrApplicationNotFound.
func (s *Service) GetMyApplication(ctx context.Context, userID uuid.UUID) (domain.Application, error) {
	a, err := s.repo.GetLatestApplicationByUser(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Application{}, domain.ErrApplicationNotFound
		}
		return domain.Application{}, err
	}
	return a, nil
}

// Approve (admin) moves a pending application to approved, creates the store, and publishes SellerApproved
// — all atomically. The role flip happens asynchronously in the identity consumer.
func (s *Service) Approve(ctx context.Context, appID uuid.UUID) (domain.Application, error) {
	a, err := s.getForDecision(ctx, appID)
	if err != nil {
		return domain.Application{}, err
	}
	if !a.CanApprove() {
		return domain.Application{}, domain.ErrNotApprovable
	}
	store := domain.Store{ID: uuid.New(), SellerID: a.UserID, Name: a.StoreName, Contact: a.Contact}
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.UpdateApplicationStatus(ctx, tx, a.ID, string(domain.StatusApproved), ""); err != nil {
			return err
		}
		if err := s.repo.InsertStore(ctx, tx, store); err != nil {
			return err
		}
		evt, err := events.NewEvent(domain.EventSellerApproved,
			domain.SellerApprovedPayload{UserID: a.UserID, ApplicationID: a.ID})
		if err != nil {
			return err
		}
		return s.outbox.Write(ctx, tx, evt) // atomic publish (P2 §6 / P3 producer pattern)
	})
	if err != nil {
		return domain.Application{}, err
	}
	a.Status = domain.StatusApproved
	return a, nil
}

// Reject (admin) moves a pending application to rejected with an optional reason. No event.
func (s *Service) Reject(ctx context.Context, appID uuid.UUID, reason string) (domain.Application, error) {
	a, err := s.getForDecision(ctx, appID)
	if err != nil {
		return domain.Application{}, err
	}
	if !a.CanReject() {
		return domain.Application{}, domain.ErrNotRejectable
	}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.UpdateApplicationStatus(ctx, tx, a.ID, string(domain.StatusRejected), reason)
	}); err != nil {
		return domain.Application{}, err
	}
	a.Status = domain.StatusRejected
	a.RejectReason = reason
	return a, nil
}

// Suspend (admin) moves an approved seller to suspended and publishes SellerSuspended (P6 hides products).
// The role stays seller — status is the source of truth.
func (s *Service) Suspend(ctx context.Context, appID uuid.UUID) (domain.Application, error) {
	a, err := s.getForDecision(ctx, appID)
	if err != nil {
		return domain.Application{}, err
	}
	if !a.CanSuspend() {
		return domain.Application{}, domain.ErrNotSuspendable
	}
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.UpdateApplicationStatus(ctx, tx, a.ID, string(domain.StatusSuspended), ""); err != nil {
			return err
		}
		evt, err := events.NewEvent(domain.EventSellerSuspended,
			domain.SellerSuspendedPayload{UserID: a.UserID, ApplicationID: a.ID})
		if err != nil {
			return err
		}
		return s.outbox.Write(ctx, tx, evt)
	})
	if err != nil {
		return domain.Application{}, err
	}
	a.Status = domain.StatusSuspended
	return a, nil
}

// GetStore returns the caller's store; none → ErrStoreNotFound.
func (s *Service) GetStore(ctx context.Context, userID uuid.UUID) (domain.Store, error) {
	st, err := s.repo.GetStoreBySeller(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Store{}, domain.ErrStoreNotFound
		}
		return domain.Store{}, err
	}
	return st, nil
}

// UpdateStore edits the caller's store. The caller must be an APPROVED seller (a suspended seller keeps
// role=seller but cannot edit → ErrNotApproved → 403).
func (s *Service) UpdateStore(ctx context.Context, userID uuid.UUID, in StoreInput) (domain.Store, error) {
	status, err := s.SellerStatus(ctx, userID)
	if err != nil {
		return domain.Store{}, err
	}
	if status != domain.StatusApproved {
		return domain.Store{}, domain.ErrNotApproved
	}
	st, err := s.repo.GetStoreBySeller(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Store{}, domain.ErrStoreNotFound
		}
		return domain.Store{}, err
	}
	updated := domain.Store{
		ID:          st.ID,
		SellerID:    userID,
		Name:        in.Name,
		LogoURL:     in.LogoURL,
		Description: in.Description,
		Contact:     in.Contact,
	}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.UpdateStore(ctx, tx, updated)
	}); err != nil {
		return domain.Store{}, err
	}
	return updated, nil
}

// SellerStatus satisfies seller.Reader — sibling modules (P6) gate on it. No active application → not a
// seller → ErrApplicationNotFound.
func (s *Service) SellerStatus(ctx context.Context, userID uuid.UUID) (domain.Status, error) {
	a, err := s.repo.GetActiveApplicationByUser(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrApplicationNotFound
		}
		return "", err
	}
	return a.Status, nil
}

// getForDecision loads an application by id for an admin action; missing → ErrApplicationNotFound.
func (s *Service) getForDecision(ctx context.Context, appID uuid.UUID) (domain.Application, error) {
	a, err := s.repo.GetApplicationByID(ctx, appID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Application{}, domain.ErrApplicationNotFound
		}
		return domain.Application{}, err
	}
	return a, nil
}
