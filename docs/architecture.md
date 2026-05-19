# Architecture

`fbs-core` is a Go HTTP service built around a small set of internal packages:

- `cmd/server`: process entrypoint, wiring, startup reconciliation, background cleanup, and graceful shutdown.
- `internal/http`: router construction, health endpoints, CORS, logging, and panic recovery middleware.
- `internal/auth`: Bearer token, AWS SigV4, dev-mode authentication, role middleware, and request principal context.
- `internal/setup`: loopback-only first-start bootstrap endpoints.
- `internal/management`: admin JSON API handlers.
- `internal/s3`: S3-compatible bucket, object, copy, delete, list, public read, and multipart handlers.
- `internal/metadata`: SQLite-backed repositories and optional metadata cache wrappers.
- `internal/storage`: local disk object storage, path validation, atomic writes, reads, deletes, and reconciliation.
- `migrations`: runtime SQLite migrations.

## Runtime Flow

Startup performs these steps:

1. Load and validate configuration from environment variables and CLI flags.
2. Open SQLite with WAL mode, foreign keys, `busy_timeout(5000)`, and `synchronous(NORMAL)`.
3. Run migrations.
4. Initialize the local disk storage engine and create the data and `.tmp` directories.
5. Reconcile object storage by deleting temporary files and orphaned object backing files.
6. Reconcile multipart temporary directories against active multipart upload rows.
7. Build authentication chains.
8. Initialize repositories, cache wrappers, handlers, public read signer, and router.
9. Start the stale multipart cleanup goroutine.
10. Start the HTTP server and wait for SIGINT or SIGTERM for graceful shutdown.

## Request Routing

The router always registers:

- `GET /healthz`
- `GET /readyz`
- `GET /api/setup/status`
- `POST /api/setup/bootstrap`
- `GET|HEAD /public/{bucket}/{key}`
- `/api/management/*`
- S3 bucket and object routes at the root

Management routes are protected by authentication and then by `admin` role authorization.

S3 routes are protected by authentication and then dispatched based on method, bucket, object key, and query parameters. The object read routes are registered separately from mutation routes so reads can keep a minimal middleware path and preserve Go's efficient file-serving behavior.

## Data Model

SQLite is the source of truth. Object bytes are only considered live when a corresponding `objects` row exists.

Objects use opaque UUID-backed storage paths instead of object keys as final filenames. The object key is retained in SQLite metadata and presented through APIs. This avoids path-length issues, removes races during overwrite, and makes metadata insertion the commit point.

## Write Consistency

Single-part object writes use this order:

1. Validate bucket and object key.
2. Stream request body to `data/.tmp/{uuid}.tmp`.
3. Sync and close the temp file.
4. Rename the temp file to a unique path under `data/{bucket}/{uuid}`.
5. Insert or upsert the object row in SQLite.
6. Delete the previous backing file if the write overwrote an existing key.

If checksum validation or metadata insertion fails, the new backing file is deleted and metadata is not committed.

Multipart completion follows the same commit-point principle:

1. Claim the upload by moving it from `active` to `completing`.
2. Validate the requested parts, order, ETags, and minimum part sizes.
3. Assemble parts into a new UUID-backed object file.
4. Atomically upsert the object row and delete the upload row in SQLite.
5. Delete previous object backing file and uploaded part files after the metadata commit.

## Authentication Model

The authentication chain supports:

- Dev mode principal when `--dev` is enabled.
- Bearer token credentials in `Authorization: Bearer {access_key_id}.{secret}`.
- AWS SigV4 header authentication.
- AWS SigV4 query-string authentication.

Authenticated requests receive an `auth.Principal` in request context. S3 uses the principal for ownership and list filtering. Management requires `Role == "admin"`.

## Caching And File Serving

The metadata layer can wrap bucket and object repositories in an in-memory cache with a configurable byte budget. Object reads use `http.ServeContent`, which provides range handling, `Last-Modified`, ETag-aware behavior, and efficient file serving through the standard library.

