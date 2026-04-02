# F1: Core HTTP Server & Routing (Backend)

**Status:** DONE

## Summary

Set up the foundational Go HTTP server with routing, configuration, middleware, and health endpoints. This feature establishes the project structure and base infrastructure all other features build upon.

## Scope

- Go project scaffolding and module structure
- HTTP router using `chi`
- Configurable bind address and public URL for ingress deployments
- Foundational middleware (logging, panic recovery, CORS)
- Health/readiness endpoints
- Tests covering health endpoints, 404 behavior, CORS preflight, and panic recovery

## Implementation Details

### Project Structure

```
cmd/server/main.go          # Entrypoint: wires config, router, server; handles graceful shutdown
internal/config/config.go   # Configuration loading from flags + env vars with validation
internal/http/router.go     # chi router setup, middleware chain, health endpoints
internal/http/router_test.go # Black-box router tests
internal/http/middleware/
  logging.go                # Request logging middleware with response recorder
  recovery.go               # Panic recovery middleware
internal/server/server.go   # HTTP server wrapper with ListenAndServe and Shutdown
doc.go                      # Module root package
```

### Configuration (`internal/config/config.go`)

Loaded via CLI flags with environment variable fallbacks:

| Setting | Flag | Env Var | Default |
|---|---|---|---|
| HTTP listen address | `--http-addr` | `FBS_HTTP_ADDR` | `127.0.0.1:9000` |
| Public base URL | `--public-base-url` | `FBS_PUBLIC_BASE_URL` | (empty) |
| CORS allowed origins | `--cors-allowed-origins` | `FBS_CORS_ALLOWED_ORIGINS` | `localhost:3000, 127.0.0.1:3000, localhost:5173, 127.0.0.1:5173` |
| Read timeout | `--read-timeout` | `FBS_READ_TIMEOUT` | `15s` |
| Write timeout | `--write-timeout` | `FBS_WRITE_TIMEOUT` | `30s` |
| Idle timeout | `--idle-timeout` | `FBS_IDLE_TIMEOUT` | `60s` |
| Shutdown timeout | `--shutdown-timeout` | `FBS_SHUTDOWN_TIMEOUT` | `10s` |

Validation enforces:
- HTTP address is non-empty
- Public base URL (if set) is a valid URI
- At least one CORS origin is configured
- All timeouts are greater than zero

### Router (`internal/http/router.go`)

- Uses `chi.NewRouter()` as the base mux
- Middleware chain (applied in order): Logging -> Recovery -> CORS
- CORS configured with allowed methods: `GET`, `HEAD`, `POST`, `PUT`, `DELETE`, `OPTIONS`
- Allowed headers include S3-relevant headers: `Authorization`, `X-Amz-Content-Sha256`, `X-Amz-Date`, `X-Amz-Security-Token`
- Exposes `ETag` via `ExposedHeaders`
- Credentials allowed, max age 300s
- Endpoints: `GET /healthz` (returns `{"status":"ok"}`), `GET /readyz` (returns `{"status":"ready"}`)
- Accepts an optional `registerExtras func(chi.Router)` callback for other features to mount additional routes

### Logging Middleware (`internal/http/middleware/logging.go`)

- Wraps `http.ResponseWriter` with a `responseRecorder` that captures status code and bytes written
- Logs: method, path, status, bytes, duration, remote_addr
- Preserves `io.ReaderFrom`, `http.Flusher`, `http.Hijacker`, `http.Pusher` interfaces via explicit delegation
- Implements `Unwrap()` for compatibility with `http.ResponseController`

### Recovery Middleware (`internal/http/middleware/recovery.go`)

- Catches panics via `defer/recover`
- Logs panic value and full stack trace
- Returns `500 Internal Server Error` plain text response

### Server (`internal/server/server.go`)

- Thin wrapper around `http.Server`
- Configures `Addr`, `Handler`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout` from config
- `ListenAndServe()` suppresses `http.ErrServerClosed` (returns nil on graceful shutdown)
- `Shutdown(ctx)` delegates to `http.Server.Shutdown`

### Entrypoint (`cmd/server/main.go`)

- Creates structured logger (`slog.TextHandler` to stdout)
- Loads config, constructs router and server
- Starts server in a goroutine
- Listens for `SIGINT`/`SIGTERM` via `signal.NotifyContext`
- On signal: initiates graceful shutdown with configured timeout
- Exits with code 1 on any error

## Test Coverage

All tests in `internal/http/router_test.go`:

| Test | What It Verifies |
|---|---|
| `TestHealthz` | `GET /healthz` returns 200, JSON content type, `{"status":"ok"}` body |
| `TestNotFound` | `GET /missing` returns 404 |
| `TestCORSPreflight` | `OPTIONS /healthz` with Origin header returns 200, correct `Access-Control-Allow-Origin` and `Access-Control-Allow-Methods` |
| `TestRecoveryMiddleware` | A panicking handler returns 500 with `Internal Server Error` body instead of crashing |

## Dependencies

- `github.com/go-chi/chi/v5` - HTTP router
- `github.com/go-chi/cors` - CORS middleware
- Go stdlib: `log/slog`, `net/http`, `flag`, `context`, `os/signal`

## Interfaces Provided to Other Features

- **`NewRouter(cfg, logger, registerExtras)`** - Other features (F6, F7, F8, F10) mount their handlers via the `registerExtras` callback
- **`config.Config`** - Shared configuration struct; will be extended by F2 (DB path), F3 (data dir), F4 (dev mode flag), etc.
- **`server.New(cfg, handler)`** - Reusable server wrapper
