package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds the DSN and pool tuning.
type Config struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	ConnectTimeout  time.Duration
}

// Open parses the DSN, applies pool settings, verifies connectivity, and returns a ready pool.
// The caller owns the pool and must Close() it.
func Open(ctx context.Context, c Config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(c.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if c.MaxConns > 0 {
		pc.MaxConns = c.MaxConns
	}
	if c.MinConns > 0 {
		pc.MinConns = c.MinConns
	}
	if c.MaxConnLifetime > 0 {
		pc.MaxConnLifetime = c.MaxConnLifetime
	}
	if c.MaxConnIdleTime > 0 {
		pc.MaxConnIdleTime = c.MaxConnIdleTime
	}

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, c.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}
