# F4: Authentication Framework & Bearer Tokens (Backend)

**Status:** TODO

## Summary

Implement the shared authentication layer for backend routes, centered on loopback-only development bypass and persistent bearer-token authentication backed by the SQLite `users` table. This feature establishes how authenticated user identity is resolved, validated, injected into request context, and enforced by downstream handlers.

## Scope

- Add a `--dev` mode that bypasses authentication for local-only development
- Restrict `--dev` mode to loopback listeners and emit a startup warning when enabled
- Implement bearer-token authentication against the `users` table
- Store bearer-token secrets as SHA-256 hashes, never plaintext
- Return raw bearer tokens once at creation time, then discard the plaintext secret
- Inject authenticated user identity into request context for downstream handlers
- Provide role-aware middleware for admin-only and general authenticated routes
- Keep the framework extensible so F5 can add SigV4 without rewriting route protection

## Prerequisites

- **F1 (Core HTTP Server & Routing):** DONE - provides the router, middleware chain, and config loading to extend
- **F2 (SQLite Metadata Layer):** TODO - provides `UserRepository`, `users` table, and persistent credential storage

## Implementation Details

### Configuration Extension

Extend `config.Config` with:

| Setting | Flag | Env Var | Default |
|---|---|---|---|
| Development auth bypass | `--dev` | `FBS_DEV` | `false` |

Validation rules:

- If `DevMode` is `true`, `HTTPAddr` must bind only to a loopback host
- Accepted loopback hosts: `127.0.0.1`, `localhost`, `::1`, `[::1]`
- Reject `0.0.0.0`, empty host binds such as `:9000`, or public interface addresses when `--dev` is enabled
- On successful startup in dev mode, log a warning that authentication is bypassed and the server is not suitable for remote exposure

Example validation helper:

```go
func validateDevMode(httpAddr string, devMode bool) error {
    if !devMode {
        return nil
    }

    host, _, err := net.SplitHostPort(httpAddr)
    if err != nil {
        return fmt.Errorf("parse http addr: %w", err)
    }

    switch strings.Trim(host, "[]") {
    case "127.0.0.1", "localhost", "::1":
        return nil
    default:
        return fmt.Errorf("--dev requires a localhost-only bind address")
    }
}
```

### Authentication Modes

F4 should establish a single framework that can support multiple authenticators:

| Mode | Purpose | Behavior |
|---|---|---|
| Dev bypass | Local development only | Inject a synthetic admin principal without checking credentials |
| Bearer token | Management API and simple homelab access | Validate `Authorization: Bearer <token>` against SQLite |
| SigV4 | Added later by F5 | Plugs into the same framework without changing route registration |

The framework should not hard-code only one authentication path into handlers. A small abstraction keeps F5 additive instead of invasive.

### Principal Model

Define the request identity type once and reuse it everywhere:

```go
type Principal struct {
    UserID      string
    DisplayName string
    AccessKeyID string
    Role        string
    DevMode     bool
}
```

Context helpers:

```go
func WithPrincipal(ctx context.Context, p Principal) context.Context
func PrincipalFromContext(ctx context.Context) (Principal, bool)
```

Downstream code should never parse `Authorization` headers directly. It should read the resolved `Principal` from context.

### Bearer Token Shape

Use a two-part token so the database can do an indexed lookup before comparing the secret hash:

```text
<access_key_id>.<secret>
```

Suggested generation format:

- `access_key_id`: random, URL-safe identifier stored in `users.access_key_id`
- `secret`: random 32-byte secret shown once to the caller
- `secret_hash`: hex-encoded SHA-256 of the secret stored in `users.secret_hash`

Example raw token returned once:

```text
fbsa_01HV4KJ8SJ3H0YV8K8B2M7A5S6.vjF2I9mQmTWwQ3y8v5bRMLdD7u0SM2q4n8mC
```

Why this shape:

- `access_key_id` supports efficient indexed lookup via `GetByAccessKeyID`
- Only the secret portion needs hashing and constant-time comparison
- The raw token remains a single opaque bearer string for clients

### Token Generation and Persistence

F4 should provide helper logic for token creation even if the actual HTTP endpoint arrives in F10.

Suggested flow:

1. Generate `access_key_id`
2. Generate random secret bytes
3. Encode secret as URL-safe/base62/base64url text
4. Compute `sha256(secret)` and hex-encode it
5. Persist a `User` row with `access_key_id`, `secret_hash`, `role`, and `is_active=1`
6. Return `<access_key_id>.<secret>` exactly once to the caller

Suggested helper contract:

```go
type IssuedToken struct {
    AccessKeyID string
    RawToken    string
    SecretHash  string
}

func IssueBearerToken(displayName, role string) (IssuedToken, error)
```

The helper should not log raw secrets. Any logs should reference only `access_key_id`, `user_id`, or display name.

### Bearer Authentication Flow

For non-dev requests protected by bearer auth:

1. Read the `Authorization` header
2. Require the `Bearer` scheme
3. Split the token on the first `.` into `access_key_id` and `secret`
4. Load the user via `UserRepository.GetByAccessKeyID`
5. Reject if the user does not exist or `is_active = false`
6. Compute `sha256(secret)`
7. Constant-time compare the computed digest against the stored `secret_hash`
8. On success, inject a `Principal` into request context and continue

Comparison must use `subtle.ConstantTimeCompare` after decoding both values to raw bytes.

Example verifier:

```go
func verifySecret(secret, storedHex string) bool {
    sum := sha256.Sum256([]byte(secret))

    expected, err := hex.DecodeString(storedHex)
    if err != nil {
        return false
    }

    return subtle.ConstantTimeCompare(sum[:], expected) == 1
}
```

### Middleware Design

The middleware should be transport-agnostic. F10 will want JSON auth failures, while S3 routes in F6/F7/F8 will want XML errors.

Recommended shape:

```go
type UnauthorizedResponder func(w http.ResponseWriter, r *http.Request, err error)

func RequireAuthentication(authenticator Authenticator, onError UnauthorizedResponder) func(http.Handler) http.Handler
func RequireRole(role string, onError UnauthorizedResponder) func(http.Handler) http.Handler
```

This avoids baking management-API error formatting into the core auth package.

### Authenticator Interface

Keep F4 extensible so F5 can register SigV4 validation later:

```go
type Authenticator interface {
    Authenticate(r *http.Request) (Principal, error)
}

type ChainAuthenticator struct {
    Authenticators []Authenticator
}
```

Evaluation order should be:

1. Dev authenticator
2. Bearer authenticator
3. Future SigV4 authenticator from F5

The first success wins. A request with no applicable auth should return an authentication error, not anonymous access.

### Dev Authenticator

When `DevMode` is enabled, inject a synthetic principal such as:

```go
Principal{
    UserID:      "dev-user",
    DisplayName: "Development User",
    AccessKeyID: "dev",
    Role:        "admin",
    DevMode:     true,
}
```

Behavioral notes:

- Health endpoints remain public regardless of auth mode
- Protected routes should behave exactly as if a real admin user authenticated
- The synthetic identity should be obvious in logs to avoid confusion during debugging

### Route Integration

The router from F1 already supports `registerExtras(func(chi.Router))`. F4 should be mounted by other features, not by itself.

Example:

```go
router := httpapi.NewRouter(cfg, logger, func(r chi.Router) {
    r.Route("/api", func(api chi.Router) {
        api.Use(auth.RequireAuthentication(authChain, writeJSONAuthError))
        api.Use(auth.RequireRole("admin", writeJSONAuthError))
        registerManagementRoutes(api)
    })

    r.Group(func(s3 chi.Router) {
        s3.Use(auth.RequireAuthentication(authChain, writeS3AuthError))
        registerS3Routes(s3)
    })
})
```

This keeps route ownership with the feature that defines the endpoints while centralizing identity resolution in F4.

### Failure Handling

| Scenario | HTTP Status | Notes |
|---|---|---|
| Missing `Authorization` header | `401 Unauthorized` | Include `WWW-Authenticate: Bearer realm="fbs"` |
| Unsupported auth scheme | `401 Unauthorized` | Do not accept Basic or custom schemes |
| Malformed bearer token | `401 Unauthorized` | Missing delimiter or empty segments |
| Unknown `access_key_id` | `401 Unauthorized` | Avoid leaking which part was invalid |
| Inactive user | `403 Forbidden` or `401 Unauthorized` | Prefer `403` once identity is known |
| Wrong secret | `401 Unauthorized` | Use constant-time compare |
| Authenticated but wrong role | `403 Forbidden` | Separate authn from authz |
| `--dev` on non-loopback bind | startup failure | Reject during config validation |

### Package Structure

```text
internal/auth/
  principal.go        # Principal type and context helpers
  authenticator.go    # Authenticator interface and chain composition
  bearer.go           # Bearer token parsing and verification
  token.go            # Token issuance helpers and hashing
  middleware.go       # RequireAuthentication and RequireRole middleware
  dev.go              # Dev-mode authenticator and loopback checks
  errors.go           # Sentinel auth errors
  bearer_test.go      # Bearer parsing and verification tests
  middleware_test.go  # Context injection and role enforcement tests
  dev_test.go         # Dev-mode validation tests
```

### Test Plan

| Test | What It Verifies |
|---|---|
| `TestDevModeRequiresLoopback` | `--dev` rejects non-local bind addresses |
| `TestDevAuthenticatorInjectsAdminPrincipal` | Protected routes receive a synthetic admin principal in dev mode |
| `TestBearerAuthSuccess` | Valid bearer token resolves a user and injects the correct principal |
| `TestBearerAuthMissingHeader` | Missing `Authorization` returns `401` with `WWW-Authenticate` |
| `TestBearerAuthMalformedToken` | Tokens without `access_key_id.secret` format are rejected |
| `TestBearerAuthUnknownUser` | Unknown `access_key_id` returns `401` |
| `TestBearerAuthInactiveUser` | Disabled users cannot authenticate |
| `TestBearerAuthWrongSecret` | Incorrect secret is rejected even when `access_key_id` exists |
| `TestRequireRole` | Authenticated users without the required role receive `403` |
| `TestIssueBearerToken` | Raw token is returned once and persisted hash matches the secret portion |
| `TestNoPlaintextSecretLogging` | Token issuance/auth paths do not emit the raw secret to logs |

Tests should use a temp SQLite database through F2's repositories rather than mocks whenever practical.

## Dependencies

- Go stdlib: `crypto/sha256`, `crypto/subtle`, `encoding/hex`, `net`, `net/http`, `context`
- `github.com/google/uuid` or a similar random ID generator for token issuance
- F2 repository interfaces for user lookup and persistence

## Interfaces Provided to Other Features

- **`RequireAuthentication(...)`** - consumed by F6/F7/F8/F10 to protect routes
- **`RequireRole(...)`** - consumed by F10 for admin-only management operations
- **`PrincipalFromContext(...)`** - consumed by handlers that need the authenticated user identity
- **`IssueBearerToken(...)`** - consumed by F10 when creating admin or member credentials
- **`Authenticator` / `ChainAuthenticator`** - extended by F5 to add SigV4 without changing route registration
