package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "github.com/amoorihesham/eco-api/internal/modules/identity"
	"github.com/amoorihesham/eco-api/internal/modules/seller/domain"
	"github.com/amoorihesham/eco-api/internal/modules/seller/service"
)

// fakeRepo returns a canned application; only the read methods used before a tx are exercised here.
type fakeRepo struct {
	app domain.Application
	err error
}

func (f fakeRepo) GetApplicationByID(context.Context, uuid.UUID) (domain.Application, error) {
	return f.app, f.err
}
func (f fakeRepo) GetActiveApplicationByUser(context.Context, uuid.UUID) (domain.Application, error) {
	return f.app, f.err
}
func (f fakeRepo) GetLatestApplicationByUser(context.Context, uuid.UUID) (domain.Application, error) {
	return f.app, f.err
}
func (fakeRepo) InsertApplication(context.Context, pgx.Tx, domain.Application) error { return nil }
func (fakeRepo) UpdateApplicationStatus(context.Context, pgx.Tx, uuid.UUID, string, string) error {
	return nil
}
func (fakeRepo) InsertStore(context.Context, pgx.Tx, domain.Store) error { return nil }
func (fakeRepo) GetStoreBySeller(context.Context, uuid.UUID) (domain.Store, error) {
	return domain.Store{}, pgx.ErrNoRows
}
func (fakeRepo) UpdateStore(context.Context, pgx.Tx, domain.Store) error { return nil }

// fakeReader stands in for identity.Reader.
type fakeReader struct {
	role string
	err  error
}

func (f fakeReader) UserByID(context.Context, uuid.UUID) (identity.PublicUser, error) {
	return identity.PublicUser{Role: f.role}, f.err
}

func TestApproveRejectsNonPending(t *testing.T) {
	repo := fakeRepo{app: domain.Application{Status: domain.StatusApproved}}
	// pool/outbox are nil: Approve returns on the guard before any transaction.
	svc := service.New(nil, repo, fakeReader{role: "buyer"}, nil)
	if _, err := svc.Approve(context.Background(), uuid.New()); !errors.Is(err, domain.ErrNotApprovable) {
		t.Fatalf("want ErrNotApprovable, got %v", err)
	}
}

func TestSuspendRequiresApproved(t *testing.T) {
	repo := fakeRepo{app: domain.Application{Status: domain.StatusPending}}
	svc := service.New(nil, repo, fakeReader{role: "buyer"}, nil)
	if _, err := svc.Suspend(context.Background(), uuid.New()); !errors.Is(err, domain.ErrNotSuspendable) {
		t.Fatalf("want ErrNotSuspendable, got %v", err)
	}
}

func TestApplyRejectsExistingSeller(t *testing.T) {
	svc := service.New(nil, fakeRepo{}, fakeReader{role: "seller"}, nil)
	if _, err := svc.Apply(context.Background(), uuid.New(), service.ApplicationInput{StoreName: "S", Contact: "c"}); !errors.Is(err, domain.ErrAlreadySeller) {
		t.Fatalf("want ErrAlreadySeller, got %v", err)
	}
}
