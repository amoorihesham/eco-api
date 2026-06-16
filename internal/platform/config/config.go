package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment string

	HTTPPort            string
	HTTPReadTimeout     time.Duration
	HTTPWriteTimeout    time.Duration
	HTTPIdleTimeout     time.Duration
	HTTPShutdownTimeout time.Duration

	LogLevel  string
	LogFormat string

	DatabaseURL string
}

func Load() (Config, error) {
	c := Config{
		Environment:         env("ENVIRONMENT", "development"),
		HTTPPort:            env("HTTP_PORT", "8080"),
		LogLevel:            env("LOG_LEVEL", "info"),
		LogFormat:           env("LOG_FORMAT", "json"),
		DatabaseURL:         env("DATABASE_URL", ""),
		HTTPReadTimeout:     envDur("HTTP_READ_TIMEOUT", 5*time.Second),
		HTTPWriteTimeout:    envDur("HTTP_WRITE_TIMEOUT", 10*time.Second),
		HTTPIdleTimeout:     envDur("HTTP_IDLE_TIMEOUT", 120*time.Second),
		HTTPShutdownTimeout: envDur("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) Validate() error {
	if !oneOf(c.Environment, "development", "staging", "production") {
		return fmt.Errorf("Environment must be one of: development, staging, production, got %s", c.Environment)
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
	return nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
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
