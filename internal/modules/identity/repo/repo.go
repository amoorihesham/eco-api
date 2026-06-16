// Package repo adapts the identity module's service.Repository port to
// sqlc-generated Postgres queries.
package repo

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/modules/identity/repo/identitydb"
)

// Repo implements service.Repository over sqlc-generated queries.
type Repo struct{ q *identitydb.Queries }

// New builds a Repo backed by pool.
func New(pool *pgxpool.Pool) *Repo { return &Repo{q: identitydb.New(pool)} }

// CreateUser inserts u within tx.
func (r *Repo) CreateUser(ctx context.Context, tx pgx.Tx, u domain.User) error {
	return r.q.WithTx(tx).CreateUser(ctx, identitydb.CreateUserParams{
		ID:           u.ID,
		Email:        u.Email,
		PasswordHash: u.PasswordHash,
		Name:         u.Name,
		Role:         string(u.Role),
		CreatedAt:    u.CreatedAt,
	})
}

// GetUserByEmail looks up a user by email.
func (r *Repo) GetUserByEmail(ctx context.Context, email string) (domain.User, error) {
	row, err := r.q.GetUserByEmail(ctx, email)
	if err != nil {
		return domain.User{}, err
	}
	return toUser(row.ID, row.Email, row.PasswordHash, row.Name, row.Role, row.CreatedAt), nil
}

// GetUserByID looks up a user by ID.
func (r *Repo) GetUserByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	row, err := r.q.GetUserByID(ctx, id)
	if err != nil {
		return domain.User{}, err
	}
	return toUser(row.ID, row.Email, row.PasswordHash, row.Name, row.Role, row.CreatedAt), nil
}

// UpdatePasswordHash sets userID's password hash within tx.
func (r *Repo) UpdatePasswordHash(ctx context.Context, tx pgx.Tx, userID uuid.UUID, hash string) error {
	return r.q.WithTx(tx).UpdatePasswordHash(ctx, identitydb.UpdatePasswordHashParams{ID: userID, PasswordHash: hash})
}

// InsertRefreshToken persists rt within tx.
func (r *Repo) InsertRefreshToken(ctx context.Context, tx pgx.Tx, rt domain.RefreshToken) error {
	return r.q.WithTx(tx).InsertRefreshToken(ctx, identitydb.InsertRefreshTokenParams{
		ID: rt.ID, UserID: rt.UserID, TokenHash: rt.TokenHash, ExpiresAt: rt.ExpiresAt,
	})
}

// GetRefreshToken looks up a refresh token by its hash.
func (r *Repo) GetRefreshToken(ctx context.Context, tokenHash string) (domain.RefreshToken, error) {
	row, err := r.q.GetRefreshToken(ctx, tokenHash)
	if err != nil {
		return domain.RefreshToken{}, err
	}
	return domain.RefreshToken{ID: row.ID, UserID: row.UserID, TokenHash: row.TokenHash, ExpiresAt: row.ExpiresAt}, nil
}

// DeleteRefreshToken removes the refresh token matching tokenHash within tx.
func (r *Repo) DeleteRefreshToken(ctx context.Context, tx pgx.Tx, tokenHash string) error {
	return r.q.WithTx(tx).DeleteRefreshToken(ctx, tokenHash)
}

// DeleteUserRefreshTokens removes all of userID's refresh tokens within tx.
func (r *Repo) DeleteUserRefreshTokens(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	return r.q.WithTx(tx).DeleteUserRefreshTokens(ctx, userID)
}

// InsertPasswordReset persists pr within tx.
func (r *Repo) InsertPasswordReset(ctx context.Context, tx pgx.Tx, pr domain.PasswordReset) error {
	return r.q.WithTx(tx).InsertPasswordReset(ctx, identitydb.InsertPasswordResetParams{
		ID: pr.ID, UserID: pr.UserID, TokenHash: pr.TokenHash, ExpiresAt: pr.ExpiresAt,
	})
}

// GetActivePasswordReset looks up an unused, unexpired password reset by
// its token hash.
func (r *Repo) GetActivePasswordReset(ctx context.Context, tokenHash string) (domain.PasswordReset, error) {
	row, err := r.q.GetActivePasswordReset(ctx, tokenHash)
	if err != nil {
		return domain.PasswordReset{}, err
	}
	return domain.PasswordReset{ID: row.ID, UserID: row.UserID, TokenHash: row.TokenHash, ExpiresAt: row.ExpiresAt}, nil
}

// MarkPasswordResetUsed marks the password reset id as used within tx.
func (r *Repo) MarkPasswordResetUsed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	return r.q.WithTx(tx).MarkPasswordResetUsed(ctx, id)
}

func toUser(id uuid.UUID, email, hash, name, role string, createdAt time.Time) domain.User {
	return domain.User{ID: id, Email: email, PasswordHash: hash, Name: name, Role: domain.Role(role), CreatedAt: createdAt}
}

// --- account (P4) ---

// UpdateUserName sets userID's display name within tx and returns the fresh row.
func (r *Repo) UpdateUserName(ctx context.Context, tx pgx.Tx, userID uuid.UUID, name string) (domain.User, error) {
	row, err := r.q.WithTx(tx).UpdateUserName(ctx, identitydb.UpdateUserNameParams{ID: userID, Name: name})
	if err != nil {
		return domain.User{}, err
	}
	return toUser(row.ID, row.Email, row.PasswordHash, row.Name, row.Role, row.CreatedAt), nil
}

// ListAddresses returns userID's addresses, default first.
func (r *Repo) ListAddresses(ctx context.Context, userID uuid.UUID) ([]domain.Address, error) {
	rows, err := r.q.ListAddresses(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Address, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAddress(row))
	}
	return out, nil
}

// GetAddress looks up an owner-scoped address by id.
func (r *Repo) GetAddress(ctx context.Context, userID, id uuid.UUID) (domain.Address, error) {
	row, err := r.q.GetAddress(ctx, identitydb.GetAddressParams{ID: id, UserID: userID})
	if err != nil {
		return domain.Address{}, err
	}
	return toAddress(row), nil
}

// CountAddresses counts userID's addresses.
func (r *Repo) CountAddresses(ctx context.Context, userID uuid.UUID) (int, error) {
	n, err := r.q.CountAddresses(ctx, userID)
	return int(n), err
}

// InsertAddress persists a within tx.
func (r *Repo) InsertAddress(ctx context.Context, tx pgx.Tx, a domain.Address) error {
	return r.q.WithTx(tx).InsertAddress(ctx, identitydb.InsertAddressParams{
		ID:         a.ID,
		UserID:     a.UserID,
		Recipient:  a.Recipient,
		Line1:      a.Line1,
		Line2:      a.Line2,
		City:       a.City,
		Region:     a.Region,
		PostalCode: a.PostalCode,
		Country:    a.Country,
		Phone:      a.Phone,
		IsDefault:  a.IsDefault,
	})
}

// UpdateAddress overwrites an owner-scoped address's fields within tx.
func (r *Repo) UpdateAddress(ctx context.Context, tx pgx.Tx, a domain.Address) error {
	return r.q.WithTx(tx).UpdateAddress(ctx, identitydb.UpdateAddressParams{
		ID:         a.ID,
		UserID:     a.UserID,
		Recipient:  a.Recipient,
		Line1:      a.Line1,
		Line2:      a.Line2,
		City:       a.City,
		Region:     a.Region,
		PostalCode: a.PostalCode,
		Country:    a.Country,
		Phone:      a.Phone,
		IsDefault:  a.IsDefault,
	})
}

// DeleteAddress removes an owner-scoped address within tx, returning the number of rows affected.
func (r *Repo) DeleteAddress(ctx context.Context, tx pgx.Tx, userID, id uuid.UUID) (int64, error) {
	return r.q.WithTx(tx).DeleteAddress(ctx, identitydb.DeleteAddressParams{ID: id, UserID: userID})
}

// ClearDefaultAddresses demotes userID's current default (if any) within tx.
func (r *Repo) ClearDefaultAddresses(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	return r.q.WithTx(tx).ClearDefaultAddresses(ctx, userID)
}

// SetAddressDefault promotes an owner-scoped address to default within tx.
func (r *Repo) SetAddressDefault(ctx context.Context, tx pgx.Tx, userID, id uuid.UUID) error {
	return r.q.WithTx(tx).SetAddressDefault(ctx, identitydb.SetAddressDefaultParams{ID: id, UserID: userID})
}

// NewestAddressID returns the id of userID's most recently created address, within tx so it sees
// uncommitted writes earlier in the same transaction.
func (r *Repo) NewestAddressID(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (uuid.UUID, error) {
	return r.q.WithTx(tx).NewestAddressID(ctx, userID)
}

func toAddress(row identitydb.IdentityAddress) domain.Address {
	return domain.Address{
		ID:         row.ID,
		UserID:     row.UserID,
		Recipient:  row.Recipient,
		Line1:      row.Line1,
		Line2:      row.Line2,
		City:       row.City,
		Region:     row.Region,
		PostalCode: row.PostalCode,
		Country:    row.Country,
		Phone:      row.Phone,
		IsDefault:  row.IsDefault,
		CreatedAt:  row.CreatedAt,
	}
}
