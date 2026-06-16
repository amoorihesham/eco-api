package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amoorihesham/eco-api/internal/modules/identity/domain"
	"github.com/amoorihesham/eco-api/internal/platform/auth"
	"github.com/amoorihesham/eco-api/internal/platform/db"
	"github.com/amoorihesham/eco-api/internal/platform/events"
)

// Config holds the token lifetimes the service needs.
type Config struct {
	RefreshTTL time.Duration
	ResetTTL   time.Duration
}

// Service implements the identity use cases. It depends only on ports.
type Service struct {
	pool   db.Beginner
	repo   Repository
	hasher auth.Hasher
	issuer auth.TokenIssuer
	outbox Outbox
	cfg    Config
}

// New builds a Service from its dependencies and configuration.
func New(pool db.Beginner, repo Repository, hasher auth.Hasher, issuer auth.TokenIssuer, outbox Outbox, cfg Config) *Service {
	return &Service{pool: pool, repo: repo, hasher: hasher, issuer: issuer, outbox: outbox, cfg: cfg}
}

// AuthResult is what register/login/refresh hand back to the handler.
type AuthResult struct {
	User         domain.User
	AccessToken  string
	RefreshToken string // plaintext — returned to the client once, never stored
	ExpiresIn    int
}

// Register creates a new buyer account, publishes UserRegistered via the
// outbox, and issues an initial token pair, all atomically.
func (s *Service) Register(ctx context.Context, email, password, name string) (AuthResult, error) {
	if _, err := s.repo.GetUserByEmail(ctx, email); err == nil {
		return AuthResult{}, domain.ErrEmailTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return AuthResult{}, err
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return AuthResult{}, err
	}
	u := domain.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: hash,
		Name:         name,
		Role:         domain.RoleBuyer,
		CreatedAt:    time.Now().UTC(),
	}

	var result AuthResult
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.CreateUser(ctx, tx, u); err != nil {
			return err
		}
		evt, err := events.NewEvent(domain.EventUserRegistered,
			domain.UserRegisteredPayload{UserID: u.ID, Email: u.Email})
		if err != nil {
			return err
		}
		if err := s.outbox.Write(ctx, tx, evt); err != nil { // atomic publish (P2 §6)
			return err
		}
		result, err = s.issueTokens(ctx, tx, u)
		return err
	})
	if err != nil {
		return AuthResult{}, err
	}
	return result, nil
}

// Login verifies email/password and issues a new token pair.
func (s *Service) Login(ctx context.Context, email, password string) (AuthResult, error) {
	u, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthResult{}, domain.ErrInvalidCredentials // generic — no enumeration
		}
		return AuthResult{}, err
	}
	if err := s.hasher.Compare(u.PasswordHash, password); err != nil {
		return AuthResult{}, domain.ErrInvalidCredentials
	}
	var result AuthResult
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		result, err = s.issueTokens(ctx, tx, u)
		return err
	})
	if err != nil {
		return AuthResult{}, err
	}
	return result, nil
}

// Refresh rotates a valid refresh token for a new token pair.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (AuthResult, error) {
	rt, err := s.repo.GetRefreshToken(ctx, hashToken(refreshToken))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthResult{}, domain.ErrInvalidToken
		}
		return AuthResult{}, err
	}
	if time.Now().After(rt.ExpiresAt) {
		return AuthResult{}, domain.ErrInvalidToken
	}
	u, err := s.repo.GetUserByID(ctx, rt.UserID)
	if err != nil {
		return AuthResult{}, err
	}
	var result AuthResult
	err = db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.DeleteRefreshToken(ctx, tx, rt.TokenHash); err != nil { // rotate
			return err
		}
		result, err = s.issueTokens(ctx, tx, u)
		return err
	})
	if err != nil {
		return AuthResult{}, err
	}
	return result, nil
}

// Logout revokes the given refresh token.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	return db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.DeleteRefreshToken(ctx, tx, hashToken(refreshToken))
	})
}

// ForgotPassword returns a reset token if the user exists; "" otherwise. The handler ALWAYS replies 202.
func (s *Service) ForgotPassword(ctx context.Context, email string) (string, error) {
	u, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil // no account enumeration
		}
		return "", err
	}
	token, err := newOpaqueToken()
	if err != nil {
		return "", err
	}
	pr := domain.PasswordReset{
		ID:        uuid.New(),
		UserID:    u.ID,
		TokenHash: hashToken(token),
		ExpiresAt: time.Now().UTC().Add(s.cfg.ResetTTL),
	}
	if err := db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.repo.InsertPasswordReset(ctx, tx, pr)
	}); err != nil {
		return "", err
	}
	return token, nil
}

// ResetPassword consumes a valid reset token to set a new password and
// revokes all of the user's refresh tokens.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	pr, err := s.repo.GetActivePasswordReset(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrInvalidToken
		}
		return err
	}
	if time.Now().After(pr.ExpiresAt) {
		return domain.ErrInvalidToken
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	return db.RunInTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.repo.UpdatePasswordHash(ctx, tx, pr.UserID, hash); err != nil {
			return err
		}
		if err := s.repo.MarkPasswordResetUsed(ctx, tx, pr.ID); err != nil {
			return err
		}
		return s.repo.DeleteUserRefreshTokens(ctx, tx, pr.UserID) // force re-login everywhere
	})
}

// issueTokens mints an access JWT and persists a fresh (rotated) refresh token on tx.
func (s *Service) issueTokens(ctx context.Context, tx pgx.Tx, u domain.User) (AuthResult, error) {
	access, expiresIn, err := s.issuer.Issue(u.ID, string(u.Role))
	if err != nil {
		return AuthResult{}, err
	}
	refresh, err := newOpaqueToken()
	if err != nil {
		return AuthResult{}, err
	}
	rt := domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    u.ID,
		TokenHash: hashToken(refresh),
		ExpiresAt: time.Now().UTC().Add(s.cfg.RefreshTTL),
	}
	if err := s.repo.InsertRefreshToken(ctx, tx, rt); err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: u, AccessToken: access, RefreshToken: refresh, ExpiresIn: expiresIn}, nil
}

func newOpaqueToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken stores/looks-up tokens by a fast SHA-256 (the token is already high-entropy random).
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
