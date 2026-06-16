// Package repo adapts the seller module's service.Repository port to sqlc-generated Postgres queries.
package repo

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
	"github.com/amoorihesham/eco-api/internal/modules/seller/repo/sellerdb"
)

// Repo implements service.Repository over sqlc-generated queries.
type Repo struct{ q *sellerdb.Queries }

// New builds a Repo backed by pool.
func New(pool *pgxpool.Pool) *Repo { return &Repo{q: sellerdb.New(pool)} }

// InsertApplication persists a within tx.
func (r *Repo) InsertApplication(ctx context.Context, tx pgx.Tx, a domain.Application) error {
	return r.q.WithTx(tx).InsertApplication(ctx, sellerdb.InsertApplicationParams{
		ID:          a.ID,
		UserID:      a.UserID,
		Status:      string(a.Status),
		StoreName:   a.StoreName,
		Description: a.Description,
		Contact:     a.Contact,
		CreatedAt:   a.CreatedAt,
	})
}

// GetApplicationByID looks up an application by id.
func (r *Repo) GetApplicationByID(ctx context.Context, id uuid.UUID) (domain.Application, error) {
	row, err := r.q.GetApplicationByID(ctx, id)
	if err != nil {
		return domain.Application{}, err
	}
	return toApplication(row.ID, row.UserID, row.Status, row.StoreName, row.Description, row.Contact, row.RejectReason, row.CreatedAt), nil
}

// GetActiveApplicationByUser looks up userID's active (pending/approved/suspended) application.
func (r *Repo) GetActiveApplicationByUser(ctx context.Context, userID uuid.UUID) (domain.Application, error) {
	row, err := r.q.GetActiveApplicationByUser(ctx, userID)
	if err != nil {
		return domain.Application{}, err
	}
	return toApplication(row.ID, row.UserID, row.Status, row.StoreName, row.Description, row.Contact, row.RejectReason, row.CreatedAt), nil
}

// GetLatestApplicationByUser looks up userID's most recently created application (any status).
func (r *Repo) GetLatestApplicationByUser(ctx context.Context, userID uuid.UUID) (domain.Application, error) {
	row, err := r.q.GetLatestApplicationByUser(ctx, userID)
	if err != nil {
		return domain.Application{}, err
	}
	return toApplication(row.ID, row.UserID, row.Status, row.StoreName, row.Description, row.Contact, row.RejectReason, row.CreatedAt), nil
}

// UpdateApplicationStatus sets an application's status (and reject reason) within tx.
func (r *Repo) UpdateApplicationStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status, rejectReason string) error {
	return r.q.WithTx(tx).UpdateApplicationStatus(ctx, sellerdb.UpdateApplicationStatusParams{
		ID: id, Status: status, RejectReason: rejectReason,
	})
}

// InsertStore persists s within tx.
func (r *Repo) InsertStore(ctx context.Context, tx pgx.Tx, s domain.Store) error {
	return r.q.WithTx(tx).InsertStore(ctx, sellerdb.InsertStoreParams{
		ID:          s.ID,
		SellerID:    s.SellerID,
		Name:        s.Name,
		LogoUrl:     s.LogoURL,
		Description: s.Description,
		Contact:     s.Contact,
	})
}

// GetStoreBySeller looks up a store by its seller (= identity user) id.
func (r *Repo) GetStoreBySeller(ctx context.Context, sellerID uuid.UUID) (domain.Store, error) {
	row, err := r.q.GetStoreBySeller(ctx, sellerID)
	if err != nil {
		return domain.Store{}, err
	}
	return domain.Store{
		ID:          row.ID,
		SellerID:    row.SellerID,
		Name:        row.Name,
		LogoURL:     row.LogoUrl,
		Description: row.Description,
		Contact:     row.Contact,
	}, nil
}

// UpdateStore overwrites a store's editable fields within tx.
func (r *Repo) UpdateStore(ctx context.Context, tx pgx.Tx, s domain.Store) error {
	return r.q.WithTx(tx).UpdateStore(ctx, sellerdb.UpdateStoreParams{
		SellerID:    s.SellerID,
		Name:        s.Name,
		LogoUrl:     s.LogoURL,
		Description: s.Description,
		Contact:     s.Contact,
	})
}

func toApplication(id, userID uuid.UUID, status, storeName, description, contact, rejectReason string, createdAt time.Time) domain.Application {
	return domain.Application{
		ID:           id,
		UserID:       userID,
		Status:       domain.Status(status),
		StoreName:    storeName,
		Description:  description,
		Contact:      contact,
		RejectReason: rejectReason,
		CreatedAt:    createdAt,
	}
}
