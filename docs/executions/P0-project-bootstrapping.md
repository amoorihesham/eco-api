# Execution Plan — P0: Project Bootstrapping

| | |
|---|---|
| **Phase** | P0 — Project Bootstrapping (see [../IMPLEMENTATION_PLAN.md](../IMPLEMENTATION_PLAN.md)) |
| **Status** | Ready to implement |
| **Date** | 2026-06-15 |
| **Outcome** | A runnable, well-structured "walking skeleton": the service boots, serves `/healthz` + `/readyz`, has the shared HTTP plumbing every later module reuses, and a green local pipeline. |
| **Module path** | `eco-api` |

> This is an **execution document**: detailed enough to implement directly. Code blocks are working
> skeletons — type them in, adjust names to taste. Companion docs: [PRD](../PRD.md) ·
> [ARCHITECTURE](../ARCHITECTURE.md) · [OpenAPI](../../api/openapi.yaml).

---

## 1. Overview

**Objective.** Establish the project foundation once, so every later phase plugs into a consistent
shape. P0 contains **no business logic and no real database access** — only the platform plumbing.

**In scope**
- Repo + Go module + the agreed directory layout.
- Typed configuration from environment (stdlib, explicit).
- Structured logging (`log/slog`).
- HTTP server (stdlib `net/http`), router (1.22 method+path mux), middleware (request id, logger, recoverer).
- The **standardized response/error envelope** + pagination helpers (mirrors the OpenAPI contract).
- Health probes: `/healthz` (liveness) and `/readyz` (readiness via a **check registry**, empty for now).
- Graceful shutdown.
- Taskfile, docker-compose (Postgres), golangci-lint config, tests.

**Out of scope (later phases)**
- Real DB connection, migrations, sqlc → **P1**.
- Event bus / outbox → **P2**.
- Any business module (auth, catalog, …) → **P3+**.
- `DATABASE_URL` is added to config/compose now but is **optional** until P1 makes it required.

---

## 2. Prerequisites (Windows / PowerShell)

| Tool | Min version | Install (PowerShell) | Verify |
|---|---|---|---|
| Go | 1.22+ (use latest stable, e.g. 1.24) | `winget install GoLang.Go` | `go version` |
| Docker Desktop | current | `winget install Docker.DockerDesktop` | `docker version` |
| go-task | v3 | `go install github.com/go-task/task/v3/cmd/task@latest` | `task --version` |
| golangci-lint | v2 | `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest` | `golangci-lint version` |

Make sure the Go bin dir is on `PATH` (so `task`/`golangci-lint` resolve):

```powershell
$env:Path += ";$(go env GOPATH)\bin"
# persist for new shells:
[Environment]::SetEnvironmentVariable("Path", $env:Path, "User")
```

---

## 3. Tech stack & versions

| Concern | Choice |
|---|---|
| Language | Go (latest stable; requires ≥1.22 for `net/http` routing) |
| HTTP | stdlib `net/http` + `http.ServeMux` |
| Logging | stdlib `log/slog` |
| Config | stdlib (`os`, `strconv`, `time`) — explicit typed loader, **zero deps** |
| Request IDs | stdlib `crypto/rand` (no uuid dep yet) |
| Task runner | Taskfile (go-task) |
| Lint | golangci-lint v2 |
| Local DB | `postgres:16-alpine` via docker-compose (not used by app until P1) |

> P0 has **zero external Go dependencies** — `go.sum` will not exist yet, which is expected.

---

## 4. Target file tree (end state of P0)

```text
eco-api/
├── go.mod
├── Taskfile.yml
├── docker-compose.yml
├── Dockerfile
├── .golangci.yml
├── .env.example
├── .gitignore
├── .dockerignore
├── README.md
├── cmd/
│   └── api/
│       └── main.go                 # composition root
├── internal/
│   └── platform/
│       ├── config/
│       │   ├── config.go
│       │   └── config_test.go
│       ├── log/
│       │   └── log.go
│       ├── httpx/
│       │   ├── response.go         # JSON + list + pagination writers
│       │   ├── pagination.go       # query parsing
│       │   ├── errors.go           # error envelope + codes
│       │   ├── middleware.go       # requestID / logger / recoverer / Chain
│       │   ├── server.go           # http.Server + graceful Run(ctx)
│       │   └── response_test.go
│       └── health/
│           ├── health.go           # /healthz + /readyz + check registry
│           └── health_test.go
├── api/                            # openapi.yaml (already present)
├── docs/                           # PRD, ARCHITECTURE, IMPLEMENTATION_PLAN, executions/
└── migrations/
    └── .gitkeep                    # populated in P1
```

**Import-direction rule (enforced from day one):** `httpx` imports only the stdlib. `health` imports
`httpx`. The router that references `health` is assembled in `cmd/api/main.go` — **never** in `httpx`
— to avoid an `httpx ↔ health` import cycle.

---

## 5. Execution steps

Work top to bottom; each step ends in a check.

### S1 — Initialize repo & module
```powershell
git init
go mod init eco-api
New-Item -ItemType Directory -Force cmd/api, internal/platform/config, internal/platform/log, internal/platform/httpx, internal/platform/health, migrations | Out-Null
if (-not (Test-Path migrations/.gitkeep)) { New-Item -ItemType File migrations/.gitkeep | Out-Null }
```
**Check:** `go.mod` exists with `module eco-api`.

### S2 — Taskfile & dev tooling
Create `Taskfile.yml` (§8) and `.golangci.yml` (§8). Install tools if not already: `task tools`.
**Check:** `task --list` prints the task set.

### S3 — Configuration loader
`internal/platform/config/config.go`:

```go
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully-resolved application configuration.
type Config struct {
	AppEnv string // dev | staging | prod

	HTTPPort            string
	HTTPReadTimeout     time.Duration
	HTTPWriteTimeout    time.Duration
	HTTPIdleTimeout     time.Duration
	HTTPShutdownTimeout time.Duration

	LogLevel  string // debug | info | warn | error
	LogFormat string // json | text

	DatabaseURL string // optional in P0; required from P1
}

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	c := Config{
		AppEnv:              env("APP_ENV", "dev"),
		HTTPPort:            env("HTTP_PORT", "8080"),
		HTTPReadTimeout:     envDur("HTTP_READ_TIMEOUT", 5*time.Second),
		HTTPWriteTimeout:    envDur("HTTP_WRITE_TIMEOUT", 10*time.Second),
		HTTPIdleTimeout:     envDur("HTTP_IDLE_TIMEOUT", 120*time.Second),
		HTTPShutdownTimeout: envDur("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
		LogLevel:            env("LOG_LEVEL", "info"),
		LogFormat:           env("LOG_FORMAT", "json"),
		DatabaseURL:         env("DATABASE_URL", ""),
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate enforces allowed values; fail fast at startup.
func (c Config) Validate() error {
	if !oneOf(c.AppEnv, "dev", "staging", "prod") {
		return fmt.Errorf("APP_ENV must be dev|staging|prod, got %q", c.AppEnv)
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
```
**Check:** `go build ./internal/platform/config` compiles.

### S4 — Logging
`internal/platform/log/log.go`:

```go
package log

import (
	"log/slog"
	"os"
	"strings"
)

// New builds the application logger from config values.
func New(level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	var h slog.Handler
	if strings.EqualFold(format, "text") {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
```
**Check:** compiles.

### S5 — Response & error envelope + pagination
`internal/platform/httpx/response.go`:

```go
package httpx

import (
	"encoding/json"
	"net/http"
)

// WriteJSON writes v as JSON with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// Pagination mirrors the OpenAPI Pagination schema.
type Pagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// NewPagination computes total_pages from total and page size.
func NewPagination(page, pageSize, total int) Pagination {
	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return Pagination{Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages}
}

// ListResponse is the standard collection envelope: {"data": [...], "pagination": {...}}.
type ListResponse struct {
	Data       any        `json:"data"`
	Pagination Pagination `json:"pagination"`
}

// WriteList writes a paginated collection.
func WriteList(w http.ResponseWriter, status int, data any, p Pagination) {
	WriteJSON(w, status, ListResponse{Data: data, Pagination: p})
}
```

`internal/platform/httpx/pagination.go`:

```go
package httpx

import (
	"net/http"
	"strconv"
)

const (
	defaultPage     = 1
	defaultPageSize = 20
	maxPageSize     = 100
)

// PageParams parses ?page and ?page_size with defaults and an upper bound.
func PageParams(r *http.Request) (page, pageSize int) {
	page = atoiOr(r.URL.Query().Get("page"), defaultPage)
	if page < 1 {
		page = defaultPage
	}
	pageSize = atoiOr(r.URL.Query().Get("page_size"), defaultPageSize)
	switch {
	case pageSize < 1:
		pageSize = defaultPageSize
	case pageSize > maxPageSize:
		pageSize = maxPageSize
	}
	return page, pageSize
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
```

`internal/platform/httpx/errors.go`:

```go
package httpx

import "net/http"

// ErrorCode is a machine-readable error code (mirrors OpenAPI ErrorCode).
type ErrorCode string

const (
	CodeValidation   ErrorCode = "validation_error"
	CodeUnauthorized ErrorCode = "unauthorized"
	CodeForbidden    ErrorCode = "forbidden"
	CodeNotFound     ErrorCode = "not_found"
	CodeConflict     ErrorCode = "conflict"
	CodeInternal     ErrorCode = "internal"
)

// ErrorDetail is an optional per-field validation message.
type ErrorDetail struct {
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// ErrorBody is the inner error object.
type ErrorBody struct {
	Code    ErrorCode     `json:"code"`
	Message string        `json:"message"`
	Details []ErrorDetail `json:"details,omitempty"`
}

// ErrorResponse is the envelope: {"error": {...}}.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// WriteError writes the standard error envelope.
func WriteError(w http.ResponseWriter, status int, code ErrorCode, msg string, details ...ErrorDetail) {
	WriteJSON(w, status, ErrorResponse{Error: ErrorBody{Code: code, Message: msg, Details: details}})
}

// Convenience wrappers used across handlers.
func NotFound(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusNotFound, CodeNotFound, msg)
}

func Internal(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusInternalServerError, CodeInternal, msg)
}

func Unauthorized(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusUnauthorized, CodeUnauthorized, msg)
}
```
**Check:** `go build ./internal/platform/httpx` compiles.

### S6 — Middleware (request id, logger, recoverer) + chain
`internal/platform/httpx/middleware.go`:

```go
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// Middleware is the standard decorator shape.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares so the first listed is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type ctxKey int

const requestIDKey ctxKey = iota

// RequestID ensures every request has an id (header or generated) and echoes it back.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" {
				id = newID()
			}
			w.Header().Set("X-Request-ID", id)
			ctx := context.WithValue(r.Context(), requestIDKey, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFrom returns the request id stored in the context, if any.
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// Logger logs one structured line per request, including the final status.
func Logger(l *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			l.LogAttrs(r.Context(), slog.LevelInfo, "http_request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", RequestIDFrom(r.Context())),
			)
		})
	}
}

// Recoverer converts panics into a 500 error envelope and logs the stack.
func Recoverer(l *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rv := recover(); rv != nil {
					l.LogAttrs(r.Context(), slog.LevelError, "panic_recovered",
						slog.Any("panic", rv),
						slog.String("request_id", RequestIDFrom(r.Context())),
						slog.String("stack", string(debug.Stack())),
					)
					Internal(w, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder captures the response status for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status, s.wrote = code, true
		s.ResponseWriter.WriteHeader(code)
	}
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status, s.wrote = http.StatusOK, true
	}
	return s.ResponseWriter.Write(b)
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}
```

**Chain order** (applied in `main`): `RequestID` (outermost) → `Logger` → `Recoverer` → routes.
So a recovered panic still gets logged with its final 500 status, and the id is present throughout.

### S7 — Health (liveness, readiness, check registry)
`internal/platform/health/health.go`:

```go
package health

import (
	"context"
	"net/http"

	"eco-api/internal/platform/httpx"
)

// Check is a named readiness probe; Func returns nil when healthy.
type Check struct {
	Name string
	Func func(ctx context.Context) error
}

// Handler serves liveness and readiness endpoints over a set of checks.
type Handler struct {
	checks []Check
}

// New builds a health handler. P0 registers no checks; P1 adds the DB check.
func New(checks ...Check) *Handler {
	return &Handler{checks: checks}
}

type statusResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Live reports that the process is up.
func (h *Handler) Live(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

// Ready runs all checks; any failure yields 503.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	results := make(map[string]string, len(h.checks))
	healthy := true
	for _, c := range h.checks {
		if err := c.Func(r.Context()); err != nil {
			results[c.Name] = err.Error()
			healthy = false
			continue
		}
		results[c.Name] = "ok"
	}
	if !healthy {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, statusResponse{Status: "unavailable", Checks: results})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, statusResponse{Status: "ok", Checks: results})
}
```
**Check:** `go build ./internal/platform/health` compiles.

### S8 — HTTP server + graceful shutdown
`internal/platform/httpx/server.go`:

```go
package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// ServerConfig holds the listen address and timeouts.
type ServerConfig struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// Run starts the server and blocks until ctx is cancelled, then shuts down gracefully.
func Run(ctx context.Context, l *slog.Logger, cfg ServerConfig, handler http.Handler) error {
	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		l.Info("http server listening", slog.String("addr", cfg.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		l.Info("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
```

### S9 — Composition root (`main`)
`cmd/api/main.go`:

```go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"eco-api/internal/platform/config"
	"eco-api/internal/platform/health"
	"eco-api/internal/platform/httpx"
	applog "eco-api/internal/platform/log"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Logger isn't built yet; report to stderr and exit non-zero.
		os.Stderr.WriteString("config error: " + err.Error() + "\n")
		os.Exit(1)
	}

	logger := applog.New(cfg.LogLevel, cfg.LogFormat)

	// Readiness checks are registered here as phases add dependencies (P1: Postgres).
	healthH := health.New()

	router := newRouter(logger, healthH)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srvCfg := httpx.ServerConfig{
		Addr:            ":" + cfg.HTTPPort,
		ReadTimeout:     cfg.HTTPReadTimeout,
		WriteTimeout:    cfg.HTTPWriteTimeout,
		IdleTimeout:     cfg.HTTPIdleTimeout,
		ShutdownTimeout: cfg.HTTPShutdownTimeout,
	}

	if err := httpx.Run(ctx, logger, srvCfg, router); err != nil {
		logger.Error("server exited with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}

// newRouter wires routes + middleware. Later phases mount their modules here (under /api/v1).
func newRouter(l *slog.Logger, h *health.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.Live)
	mux.HandleFunc("GET /readyz", h.Ready)
	return httpx.Chain(mux, httpx.RequestID(), httpx.Logger(l), httpx.Recoverer(l))
}
```
**Check:** `task run`, then `Invoke-RestMethod http://localhost:8080/healthz`.

### S10 — Local infra, env, ignores, tests
Create `docker-compose.yml`, `Dockerfile`, `.env.example`, `.gitignore`, `.dockerignore`, `README.md`
(§8) and the tests (§9). Copy env and bring up the DB:

```powershell
Copy-Item .env.example .env
task db:up
docker compose ps
```
**Check:** `task ci` is green; Postgres reports healthy.

---

## 6. The response / error envelope contract

These shapes must match [../../api/openapi.yaml](../../api/openapi.yaml). Every later handler reuses them.

**Error (any non-2xx)**
```json
{ "error": { "code": "validation_error", "message": "One or more fields are invalid.",
  "details": [ { "field": "email", "message": "must be a valid email address" } ] } }
```
**Collection (paginated)**
```json
{ "data": [ /* items */ ], "pagination": { "page": 1, "page_size": 20, "total": 137, "total_pages": 7 } }
```
**Single resource** — returned directly (the bare object), no wrapper.

| Concern | Type / helper |
|---|---|
| Error codes | `httpx.ErrorCode` constants: `validation_error`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `internal` |
| Write error | `httpx.WriteError(w, status, code, msg, details...)` |
| Write JSON | `httpx.WriteJSON(w, status, v)` |
| Write list | `httpx.WriteList(w, status, data, httpx.NewPagination(page, size, total))` |
| Parse paging | `page, size := httpx.PageParams(r)` |

---

## 7. Configuration reference

| Env var | Type | Default | Required from |
|---|---|---|---|
| `APP_ENV` | enum `dev\|staging\|prod` | `dev` | P0 |
| `HTTP_PORT` | int (1–65535) | `8080` | P0 |
| `HTTP_READ_TIMEOUT` | duration | `5s` | P0 |
| `HTTP_WRITE_TIMEOUT` | duration | `10s` | P0 |
| `HTTP_IDLE_TIMEOUT` | duration | `120s` | P0 |
| `HTTP_SHUTDOWN_TIMEOUT` | duration | `15s` | P0 |
| `LOG_LEVEL` | enum `debug\|info\|warn\|error` | `info` | P0 |
| `LOG_FORMAT` | enum `json\|text` | `json` | P0 |
| `DATABASE_URL` | string (DSN) | `""` | **P1** (optional in P0) |
| `POSTGRES_USER/PASSWORD/DB` | string | `eco/ecopass/eco` | P0 (compose only) |

---

## 8. Full file contents

**`go.mod`**
```text
module eco-api

go 1.24
```

**`Taskfile.yml`**
```yaml
version: '3'

dotenv: ['.env']

vars:
  APP_BIN: bin/api

tasks:
  default:
    cmds: [task --list]

  run:
    desc: Run the API locally
    cmds: [go run ./cmd/api]

  build:
    desc: Build the API binary
    cmds: [go build -o {{.APP_BIN}} ./cmd/api]

  test:
    desc: Run all tests
    cmds: [go test ./... -count=1]

  lint:
    desc: Run golangci-lint (also checks formatting)
    cmds: [golangci-lint run]

  fmt:
    desc: Format the code
    cmds: [gofmt -w .]

  tidy:
    desc: Tidy go.mod
    cmds: [go mod tidy]

  ci:
    desc: Full local pipeline (tidy, lint, test, build)
    cmds:
      - go mod tidy
      - golangci-lint run
      - go test ./... -count=1
      - go build -o {{.APP_BIN}} ./cmd/api

  db:up:
    desc: Start Postgres
    cmds: [docker compose up -d db]

  db:down:
    desc: Stop Postgres and remove containers
    cmds: [docker compose down]

  tools:
    desc: Install dev tools
    cmds:
      - go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```
> `-race` is omitted from `test` because it needs a C compiler on Windows. Add a `test:race` target
> (`go test ./... -race`) once you have gcc (MinGW) or run it under WSL.

**`docker-compose.yml`**
```yaml
services:
  db:
    image: postgres:16-alpine
    container_name: eco-api-db
    environment:
      POSTGRES_USER: ${POSTGRES_USER:-eco}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-ecopass}
      POSTGRES_DB: ${POSTGRES_DB:-eco}
    ports:
      - "5432:5432"
    volumes:
      - eco-pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER:-eco} -d ${POSTGRES_DB:-eco}"]
      interval: 5s
      timeout: 3s
      retries: 10

volumes:
  eco-pgdata:
```

**`Dockerfile`** (used from deploy/P18; fine to add now)
```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]
```

**`.golangci.yml`** (golangci-lint v2)
```yaml
version: "2"
linters:
  default: standard      # errcheck, govet, ineffassign, staticcheck, unused
  enable:
    - revive
    - misspell
    - bodyclose
formatters:
  enable:
    - gofmt
    - goimports
```

**`.env.example`**
```text
# App
APP_ENV=dev

# HTTP
HTTP_PORT=8080
HTTP_READ_TIMEOUT=5s
HTTP_WRITE_TIMEOUT=10s
HTTP_IDLE_TIMEOUT=120s
HTTP_SHUTDOWN_TIMEOUT=15s

# Logging
LOG_LEVEL=info
LOG_FORMAT=json

# Database (used from P1; compose provides this locally)
DATABASE_URL=postgres://eco:ecopass@localhost:5432/eco?sslmode=disable

# Postgres (consumed by docker-compose)
POSTGRES_USER=eco
POSTGRES_PASSWORD=ecopass
POSTGRES_DB=eco
```

**`.gitignore`**
```text
/bin/
*.exe
*.test
coverage.out
.env
.idea/
.vscode/
.DS_Store
```

**`.dockerignore`**
```text
.git
bin
.env
docs
*.md
```

---

## 9. Testing plan

| Test | File | Asserts |
|---|---|---|
| Config load + defaults + validation | `config/config_test.go` | defaults apply; bad `APP_ENV`/`HTTP_PORT` error |
| Error envelope shape | `httpx/response_test.go` | status, `Content-Type`, `{"error":{code,message}}` body |
| Health endpoints | `health/health_test.go` | `/healthz`→200 `{status:ok}`; a failing check→503 |

Representative — `internal/platform/health/health_test.go`:

```go
package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiveOK(t *testing.T) {
	rr := httptest.NewRecorder()
	New().Live(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
}

func TestReadyFailsWhenCheckFails(t *testing.T) {
	h := New(Check{Name: "dep", Func: func(context.Context) error { return errors.New("down") }})
	rr := httptest.NewRecorder()
	h.Ready(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rr.Code)
	}
}
```

Run: `task test`.

---

## 10. Definition of Done

- [ ] `task run` boots the service; logs show `http server listening`.
- [ ] `GET /healthz` → `200` with body `{"status":"ok"}`.
- [ ] `GET /readyz` → `200` (no checks registered yet).
- [ ] Invalid config (e.g. `HTTP_PORT=abc`) makes the process exit non-zero with a clear message.
- [ ] `Ctrl+C` triggers graceful shutdown logs (`shutdown signal received` → `shutdown complete`).
- [ ] `task ci` is green (tidy, lint, test, build).
- [ ] `task db:up` → `docker compose ps` shows `eco-api-db` healthy.
- [ ] Repo matches the §4 target tree; `httpx` imports only stdlib; the router lives in `main`.
- [ ] Envelope helpers match the OpenAPI `Error` + `pagination` shapes (§6).

---

## 11. Verification (PowerShell)

```powershell
# 1. Build pipeline
task ci

# 2. Run + probe (in a second terminal, after `task run`)
Invoke-RestMethod http://localhost:8080/healthz   # -> status : ok
Invoke-RestMethod http://localhost:8080/readyz    # -> status : ok
(Invoke-WebRequest http://localhost:8080/healthz).Headers['X-Request-ID']  # non-empty

# 3. Config fail-fast
$env:HTTP_PORT = "abc"; go run ./cmd/api   # exits non-zero with "HTTP_PORT invalid"
Remove-Item Env:HTTP_PORT

# 4. Local Postgres (not used by app yet)
task db:up
docker compose ps          # eco-api-db -> healthy
task db:down
```

---

## 12. Handoff to P1 (Persistence Foundation)

P1 plugs into the seams P0 created — no rework:
- **Config:** make `DATABASE_URL` **required** in `Config.Validate()`.
- **DB pool:** add `internal/platform/db` (pgx pool + `RunInTx`), constructed in `main` after config.
- **Readiness:** register a DB check — `health.New(health.Check{Name: "postgres", Func: pool.Ping})` —
  so `/readyz` reflects real dependency health.
- **Migrations & sqlc:** populate `migrations/` (golang-migrate) and add the sqlc workflow + the
  `<module>_<table>` ownership convention.
- **Repository ports:** introduce the interface-at-the-repo-boundary pattern the first module (P3) copies.
```
