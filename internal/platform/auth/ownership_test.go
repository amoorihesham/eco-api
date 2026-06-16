package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/platform/auth"
)

func TestEnsureOwner(t *testing.T) {
	owner := uuid.New()
	if err := auth.EnsureOwner(owner, owner); err != nil {
		t.Fatalf("same id should be allowed, got %v", err)
	}
	if err := auth.EnsureOwner(uuid.New(), owner); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("want ErrForbidden for a different caller, got %v", err)
	}
}

func TestUserIDFromContext(t *testing.T) {
	if _, ok := auth.UserID(context.Background()); ok {
		t.Fatal("expected ok=false for an unauthenticated context")
	}
}
