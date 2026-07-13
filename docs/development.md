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
compat/s3-tests/         optional ceph/s3-tests runner (not vendored)
```

## Tests

Run all tests:

```bash
go test ./...
```

### External S3 compatibility suite (ceph/s3-tests)

Shared team harness under `compat/s3-tests/` (tracked in git). It clones
[ceph/s3-tests](https://github.com/ceph/s3-tests) into a local `.workdir/`
and runs it against a temporary fbs-core process (or an external endpoint).

```bash
./compat/s3-tests/run.sh --keep
# writes results/last-run.log + results/checklist.md + results/checklist.html

./compat/s3-tests/run.sh -- s3tests/functional/test_s3.py::test_bucket_list_empty
./compat/s3-tests/run.sh --full
```

Open the feature checklist after a run:

- `compat/s3-tests/results/checklist.md`
- `compat/s3-tests/results/checklist.html`

Default `--core` mode applies `compat/s3-tests/markers.core`. Why each
marker is dropped or kept is documented in
[`compat/s3-tests/markers.md`](../compat/s3-tests/markers.md) (mini-IAM grants
are not AWS IAM/policy/STS — those markers stay excluded). Product scope:
[`plan/s3-compatibility.md`](../plan/s3-compatibility.md).

This is discovery and regression against AWS-like expectations, not the CI
source of truth. Claimed fbs-core behavior is covered by `go test ./...`.
Full team instructions: [`compat/s3-tests/README.md`](../compat/s3-tests/README.md).

The test suite is package-focused and covers:

- Config parsing and validation.
- Router health, CORS, recovery, and auth behavior.
- Setup bootstrap behavior.
- Management endpoint contracts.
- Bearer, SigV4, dev-mode, principal, and middleware auth behavior.
- Authz evaluator (admin/owner/grants/prefix/list rules) and grant repository behavior.
- Metadata repositories, migrations, cache behavior, multipart state, users, buckets, objects, grants, and management queries.
- Storage writes, reads, deletes, path sanitization, and reconciliation.
- S3 bucket, object, multipart, checksum, grant-scoped access, and compatibility behavior.
- Public read signing.

## Adding S3 Operations and Actions

When implementing a new S3 data-plane operation:

1. Map it to an existing action in `internal/authz/actions.go`, or add a new action there and in `plan/access-control/access-control.md`.
2. If the action should be grantable, add it to `GrantableActions` and to metadata grantable validation.
3. Call the shared authz evaluator from the handler with the correct action, bucket, object key, and list prefix.
4. Do not invent a private owner-only boolean path for normal bucket/object ops.

## Implementation Principles

The codebase keeps behavior separated by package:

- HTTP handlers translate protocol details to repository and storage calls; authorization decisions come from `internal/authz`.
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

