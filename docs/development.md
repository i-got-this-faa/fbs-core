# Development

## Repository Layout

```text
cmd/server/              server entrypoint and server-level tests
internal/auth/           authentication and authorization
internal/config/         config loading and validation
internal/http/           router and HTTP middleware
internal/management/     admin JSON API
internal/metadata/       SQLite repositories and cache wrappers
internal/objectops/      shared object operation helpers
internal/publicread/     signed public read URL support
internal/responses/      shared JSON response helpers
internal/s3/             S3-compatible API handlers
internal/s3compat/       compatibility constants
internal/server/         HTTP server wrapper
internal/setup/          first-start bootstrap API
internal/storage/        local disk storage engine
migrations/              SQLite migrations
docs/                    completed project documentation
```

## Tests

Run all tests:

```bash
go test ./...
```

The test suite is package-focused and covers:

- Config parsing and validation.
- Router health, CORS, recovery, and auth behavior.
- Setup bootstrap behavior.
- Management endpoint contracts.
- Bearer, SigV4, dev-mode, principal, and middleware auth behavior.
- Metadata repositories, migrations, cache behavior, multipart state, users, buckets, objects, and management queries.
- Storage writes, reads, deletes, path sanitization, and reconciliation.
- S3 bucket, object, multipart, checksum, and compatibility behavior.
- Public read signing.

## Implementation Principles

The codebase keeps behavior separated by package:

- HTTP handlers translate protocol details to repository and storage calls.
- Metadata repositories own SQLite queries and transactional state changes.
- Storage owns filesystem paths, temp files, file assembly, and reconciliation.
- Auth owns credential parsing and principal creation.

For new behavior, search for existing helpers before adding new abstractions. Prefer extending a narrow repository or handler helper over adding broad cross-package utilities.

## Adding S3 Behavior

When adding S3 compatibility:

- Add dispatch rules in `internal/s3/dispatch.go` only when query routing changes.
- Keep S3 XML DTOs local to the handler file that owns the operation.
- Return S3-style XML errors through `WriteS3Error`.
- Add black-box HTTP tests for status codes, headers, XML bodies, and edge cases.
- Avoid coupling tests to private implementation details when protocol behavior is the important contract.

## Adding Management Behavior

When adding Management API endpoints:

- Register routes in `internal/management/routes.go`.
- Use JSON response DTOs from `internal/management/dto.go`.
- Use `writeError` for consistent error envelopes.
- Keep responses `no-store` unless there is a deliberate reason to cache.
- Add endpoint tests in `internal/management`.

## Adding Metadata

When schema changes are needed:

1. Add a migration in `migrations/migration.go`.
2. Keep migrations idempotent where existing deployments may have partial historical schema.
3. Add repository methods in `internal/metadata`.
4. Test migration behavior and repository behavior.

SQLite remains the source of truth. Do not make object existence depend only on files on disk.

## Adding Storage Behavior

Storage paths must always resolve under the configured data directory. Use existing path validation helpers and keep object backing files independent from user-provided keys unless there is a clear reason to change the consistency model.

Writes should preserve the current commit order:

1. Write and sync bytes to disk.
2. Rename into place.
3. Commit metadata.
4. Clean old files after successful metadata commit.

