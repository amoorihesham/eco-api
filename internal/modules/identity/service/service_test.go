package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/modules/identity/service"
)

// fakeRepo implements service.Repository; only the read methods are exercised here.
type fakeRepo struct {
	user domain.User
	err  error
}

func (f fakeRepo) GetUserByEmail(_ context.Context, _ string) (domain.User, error) {
	return f.user, f.err
}
func (f fakeRepo) GetUserByID(_ context.Context, _ uuid.UUID) (domain.User, error) {
	return f.user, f.err
}
func (fakeRepo) CreateUser(context.Context, pgx.Tx, domain.User) error                 { return nil }
func (fakeRepo) UpdatePasswordHash(context.Context, pgx.Tx, uuid.UUID, string) error   { return nil }
func (fakeRepo) InsertRefreshToken(context.Context, pgx.Tx, domain.RefreshToken) error { return nil }
func (fakeRepo) GetRefreshToken(context.Context, string) (domain.RefreshToken, error) {
	return domain.RefreshToken{}, pgx.ErrNoRows
}
func (fakeRepo) DeleteRefreshToken(context.Context, pgx.Tx, string) error                { return nil }
func (fakeRepo) DeleteUserRefreshTokens(context.Context, pgx.Tx, uuid.UUID) error        { return nil }
func (fakeRepo) InsertPasswordReset(context.Context, pgx.Tx, domain.PasswordReset) error { return nil }
func (fakeRepo) GetActivePasswordReset(context.Context, string) (domain.PasswordReset, error) {
	return domain.PasswordReset{}, pgx.ErrNoRows
}
func (fakeRepo) MarkPasswordResetUsed(context.Context, pgx.Tx, uuid.UUID) error { return nil }

type fakeHasher struct{}

func (fakeHasher) Hash(p string) (string, error) { return "hash:" + p, nil }
func (fakeHasher) Compare(hash, p string) error {
	if hash == "hash:"+p {
		return nil
	}
	return errors.New("mismatch")
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	repo := fakeRepo{user: domain.User{PasswordHash: "hash:correct", Role: domain.RoleBuyer}}
	// pool/issuer/outbox are nil: Login returns before any transaction on a credential failure.
	svc := service.New(nil, repo, fakeHasher{}, nil, nil, service.Config{})

	if _, err := svc.Login(context.Background(), "a@b.com", "wrong"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials, got %v", err)
	}
}

func TestLoginRejectsUnknownEmail(t *testing.T) {
	repo := fakeRepo{err: pgx.ErrNoRows}
	svc := service.New(nil, repo, fakeHasher{}, nil, nil, service.Config{})

	if _, err := svc.Login(context.Background(), "missing@b.com", "whatever"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials (no enumeration), got %v", err)
	}
}
