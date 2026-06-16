package config

import (
	"testing"
	"time"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when DATABASE_URL is empty")
	}
}

func TestLoadDBDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://eco:ecopass@localhost:5432/eco?sslmode=disable")
	t.Setenv("AUTH_JWT_SECRET", "test-secret-at-least-32-bytes-long!!")

	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.DBMaxConns != 10 {
		t.Errorf("DBMaxConns: want 10, got %d", c.DBMaxConns)
	}
	if c.DBMinConns != 2 {
		t.Errorf("DBMinConns: want 2, got %d", c.DBMinConns)
	}
	if c.DBConnectTimeout != 5*time.Second {
		t.Errorf("DBConnectTimeout: want 5s, got %s", c.DBConnectTimeout)
	}
}

func TestLoadOutboxDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://eco:ecopass@localhost:5432/eco?sslmode=disable")
	t.Setenv("AUTH_JWT_SECRET", "test-secret-at-least-32-bytes-long!!")

	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.OutboxPollInterval != time.Second {
		t.Errorf("OutboxPollInterval: want 1s, got %s", c.OutboxPollInterval)
	}
	if c.OutboxBatchSize != 100 {
		t.Errorf("OutboxBatchSize: want 100, got %d", c.OutboxBatchSize)
	}
}

func TestLoadOutboxBatchSizeZeroIsInvalid(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://eco:ecopass@localhost:5432/eco?sslmode=disable")
	t.Setenv("AUTH_JWT_SECRET", "test-secret-at-least-32-bytes-long!!")
	t.Setenv("OUTBOX_BATCH_SIZE", "0")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when OUTBOX_BATCH_SIZE is 0")
	}
}

func TestLoadRequiresAuthJWTSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://eco:ecopass@localhost:5432/eco?sslmode=disable")
	t.Setenv("AUTH_JWT_SECRET", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when AUTH_JWT_SECRET is empty")
	}
}

func TestLoadRejectsShortAuthJWTSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://eco:ecopass@localhost:5432/eco?sslmode=disable")
	t.Setenv("AUTH_JWT_SECRET", "too-short")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when AUTH_JWT_SECRET is shorter than 32 bytes")
	}
}

func TestLoadAuthDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://eco:ecopass@localhost:5432/eco?sslmode=disable")
	t.Setenv("AUTH_JWT_SECRET", "test-secret-at-least-32-bytes-long!!")

	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.AuthAccessTTL != 15*time.Minute {
		t.Errorf("AuthAccessTTL: want 15m, got %s", c.AuthAccessTTL)
	}
	if c.AuthRefreshTTL != 720*time.Hour {
		t.Errorf("AuthRefreshTTL: want 720h, got %s", c.AuthRefreshTTL)
	}
	if c.AuthResetTTL != time.Hour {
		t.Errorf("AuthResetTTL: want 1h, got %s", c.AuthResetTTL)
	}
	if c.AuthBcryptCost != 12 {
		t.Errorf("AuthBcryptCost: want 12, got %d", c.AuthBcryptCost)
	}
}

func TestLoadAuthBcryptCostOutOfRangeIsInvalid(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://eco:ecopass@localhost:5432/eco?sslmode=disable")
	t.Setenv("AUTH_JWT_SECRET", "test-secret-at-least-32-bytes-long!!")
	t.Setenv("AUTH_BCRYPT_COST", "9")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when AUTH_BCRYPT_COST is below 10")
	}
}
