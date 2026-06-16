// Package config loads and validates process configuration from
// environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all process configuration loaded from the environment.
type Config struct {
	Environment string

	HTTPPort            string
	HTTPReadTimeout     time.Duration
	HTTPWriteTimeout    time.Duration
	HTTPIdleTimeout     time.Duration
	HTTPShutdownTimeout time.Duration

	LogLevel  string
	LogFormat string

	DatabaseURL       string
	DBMaxConns        int32
	DBMinConns        int32
	DBMaxConnLifetime time.Duration
	DBMaxConnIdleTime time.Duration
	DBConnectTimeout  time.Duration

	OutboxPollInterval time.Duration
	OutboxBatchSize    int32

	AuthJWTSecret  string
	AuthAccessTTL  time.Duration
	AuthRefreshTTL time.Duration
	AuthResetTTL   time.Duration
	AuthBcryptCost int
}

// Load reads configuration from environment variables, applying defaults
// and validating the result.
func Load() (Config, error) {
	c := Config{
		Environment:         env("ENVIRONMENT", "development"),
		HTTPPort:            env("HTTP_PORT", "8080"),
		LogLevel:            env("LOG_LEVEL", "info"),
		LogFormat:           env("LOG_FORMAT", "json"),
		HTTPReadTimeout:     envDur("HTTP_READ_TIMEOUT", 5*time.Second),
		HTTPWriteTimeout:    envDur("HTTP_WRITE_TIMEOUT", 10*time.Second),
		HTTPIdleTimeout:     envDur("HTTP_IDLE_TIMEOUT", 120*time.Second),
		HTTPShutdownTimeout: envDur("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),

		DatabaseURL:       env("DATABASE_URL", ""),
		DBMaxConns:        int32(envInt("DB_MAX_CONNS", 10)),
		DBMinConns:        int32(envInt("DB_MIN_CONNS", 2)),
		DBMaxConnLifetime: envDur("DB_MAX_CONN_LIFETIME", time.Hour),
		DBMaxConnIdleTime: envDur("DB_MAX_CONN_IDLE_TIME", 30*time.Minute),
		DBConnectTimeout:  envDur("DB_CONNECT_TIMEOUT", 5*time.Second),

		OutboxPollInterval: envDur("OUTBOX_POLL_INTERVAL", time.Second),
		OutboxBatchSize:    int32(envInt("OUTBOX_BATCH_SIZE", 100)),

		AuthJWTSecret:  env("AUTH_JWT_SECRET", ""),
		AuthAccessTTL:  envDur("AUTH_ACCESS_TTL", 15*time.Minute),
		AuthRefreshTTL: envDur("AUTH_REFRESH_TTL", 720*time.Hour),
		AuthResetTTL:   envDur("AUTH_RESET_TTL", time.Hour),
		AuthBcryptCost: envInt("AUTH_BCRYPT_COST", 12),
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate checks that all fields hold acceptable values, returning an
// error describing the first violation found.
func (c Config) Validate() error {

	if !oneOf(c.Environment, "development", "staging", "production") {
		return fmt.Errorf("ENVIRONMENT must be one of: development, staging, production, got %s", c.Environment)
	}
	if !oneOf(c.LogLevel, "debug", "info", "warn", "error") {
		return fmt.Errorf("LOG_LEVEL invalid: %q", c.LogLevel)
	}
	if !oneOf(c.LogFormat, "json", "text") {
		return fmt.Errorf("LOG_FORMAT invalid: %q", c.LogFormat)
	}
	if p, err := strconv.Atoi(c.HTTPPort); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("HTTP_PORT invalid: %q", c.HTTPPort)
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.DBMaxConns < 1 {
		return fmt.Errorf("DB_MAX_CONNS must be >= 1, got %d", c.DBMaxConns)
	}
	if c.DBMinConns < 0 || c.DBMinConns > c.DBMaxConns {
		return fmt.Errorf("DB_MIN_CONNS must be 0..DB_MAX_CONNS (%d), got %d", c.DBMaxConns, c.DBMinConns)
	}
	if c.OutboxBatchSize < 1 {
		return fmt.Errorf("OUTBOX_BATCH_SIZE must be >= 1, got %d", c.OutboxBatchSize)
	}
	if len(strings.TrimSpace(c.AuthJWTSecret)) < 32 {
		return fmt.Errorf("AUTH_JWT_SECRET is required and must be >= 32 bytes")
	}
	if c.AuthBcryptCost < 10 || c.AuthBcryptCost > 31 {
		return fmt.Errorf("AUTH_BCRYPT_COST must be 10..31, got %d", c.AuthBcryptCost)
	}
	return nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if strings.EqualFold(v, a) {
			return true
		}
	}
	return false
}
