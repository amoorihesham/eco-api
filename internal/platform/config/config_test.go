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
