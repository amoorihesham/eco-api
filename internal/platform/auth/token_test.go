package auth_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/amoorihesham/eco-api/internal/platform/auth"
)

func TestJWTRoundTrip(t *testing.T) {
	j := auth.NewJWT("test-secret-at-least-32-bytes-long!!", 15*time.Minute)
	id := uuid.New()

	tok, expiresIn, err := j.Issue(id, "admin")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if expiresIn != 900 {
		t.Fatalf("want expiresIn 900, got %d", expiresIn)
	}
	claims, err := j.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.UserID != id || claims.Role != "admin" {
		t.Fatalf("claims mismatch: %+v", claims)
	}
}

func TestJWTRejectsTampered(t *testing.T) {
	j := auth.NewJWT("test-secret-at-least-32-bytes-long!!", time.Minute)
	tok, _, _ := j.Issue(uuid.New(), "buyer")
	if _, err := j.Verify(tok + "x"); err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestJWTRejectsExpired(t *testing.T) {
	j := auth.NewJWT("test-secret-at-least-32-bytes-long!!", -time.Minute) // already expired
	tok, _, _ := j.Issue(uuid.New(), "buyer")
	if _, err := j.Verify(tok); err == nil {
		t.Fatal("expected error for expired token")
	}
}
