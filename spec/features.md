# Features & Delegation Breakdown

This document defines the discrete features required to build the S3-Compatible Storage Ecosystem. Each feature is scoped to be delegatable to a single developer or a small pair-programming team.

## F1: Core HTTP Server & Routing (Backend)
**Owner:** [Unassigned]
- Set up the Go project structure.
- Implement the HTTP router using `chi`.
- Configure a default local listener on `localhost:9000` plus configurable bind and public URL settings for ingress deployments.
- Add foundational middleware (logging, panic recovery, CORS).
- Add original router and server tests covering health endpoints, 404 behavior, CORS preflight, and panic recovery.

## F2: SQLite Metadata Layer (Backend)
**Owner:** [Unassigned]
- Design the SQLite schema (tables: `users`, `buckets`, `objects`, `multipart_uploads`).
- Configure SQLite for high concurrency (WAL mode, `busy_timeout=5000`, `synchronous=NORMAL`).
- Create Go repository interfaces for CRUD operations on metadata.
- Add `created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP` to `multipart_uploads` for stale upload tracking.

## F3: Disk Storage Engine (Backend)
**Owner:** [Unassigned]
- Implement file system operations matching the `/data/{bucket_name}/{object_key}` structure.
- Implement Atomic Writes: upload to `.tmp/UUID`, then rename on success. Data is written to disk before metadata is inserted into SQLite.
- Implement direct file reads and soft delete.
- Implement path sanitization: resolve object keys against the data root directory, reject keys containing path traversal sequences (`../`), null bytes, or filesystem-invalid characters. Create intermediate directories for keys containing `/`.
- Implement startup reconciliation: on server boot, purge all files in `.tmp/` and delete any data files on disk that have no corresponding SQLite metadata row.

## F4: Authentication Framework & Bearer Tokens (Backend)
**Owner:** [Unassigned]
- Implement the `--dev` CLI flag to bypass auth. Restrict `--dev` mode to `localhost` binding only and log a startup warning.
- Implement Bearer Token authentication against the `users` SQLite table.
- Store tokens as SHA-256 hashes in SQLite, never in plaintext. Return the raw token to the user once at creation time.
- On authentication, hash the incoming token and compare against stored hashes.
- Create middleware to inject the authenticated user context.

## F5: AWS SigV4 Authentication (Backend)
**Owner:** [Unassigned]
- Implement AWS Signature Version 4 verification for strict SDK compatibility.
- Parse headers/query params to validate signed requests.

## F6: S3 API - Basic Object Operations (Backend)
**Owner:** [Unassigned]
- Implement HTTP handlers for `PUT`, `GET`, `DELETE`, and `HEAD`.
- Wire handlers to the Disk Storage Engine (F3) and Metadata Layer (F2).
- Ensure standard XML/JSON S3 error formatting.
- On `PUT`: validate `Content-MD5` and `x-amz-checksum-*` headers using a `TeeReader` hash pipeline. Reject uploads with mismatched checksums before committing metadata. Store the computed MD5 as the object's ETag.
- On `GET`/`HEAD`: return `ETag`, `Content-Length`, and `Last-Modified` headers from stored metadata.
- Add original black-box S3 compatibility tests for `PUT`, `GET`, `DELETE`, and `HEAD`, asserting protocol behavior rather than internal implementation details.

## F7: S3 API - Bucket Operations (Backend)
**Owner:** [Unassigned]
- Implement `ListObjectsV2` endpoint.
- Query the SQLite metadata layer to return paginated directory structures.
- Add black-box compatibility tests for `ListObjectsV2`, including pagination and prefix or delimiter behavior.

## F8: S3 API - Multipart Uploads (Backend)
**Owner:** [Unassigned]
- Implement endpoints: `CreateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload`, `AbortMultipartUpload`.
- Manage temporary parts on disk and track state in SQLite.
- Implement a background goroutine that periodically purges multipart uploads older than a configurable TTL (default 24h), deleting both the on-disk parts and the SQLite tracking rows.
- Add original black-box multipart flow tests for initiate, upload part, complete, and abort behavior.

## F9: Egress & Caching Optimizations (Backend)
**Owner:** [Unassigned]
- Implement Go Memory LRU cache for hot SQLite metadata queries.
- Add standard CDN headers (`Cache-Control`, `ETag`).
- Optimize the `GET` path to use `sendfile()` (zero-copy). Use a separate `chi.Group` with a minimal middleware chain for S3 `GET` routes to avoid wrapping `http.ResponseWriter` and breaking the `io.ReaderFrom` interface. Use `http.ServeContent()` to handle sendfile, range requests, ETag, and Last-Modified in one call.
- Add a test asserting that the `GET` middleware chain preserves `io.ReaderFrom` on the `ResponseWriter`.

## F10: Management API (Backend)
**Owner:** [Unassigned]
- Expose RESTful JSON endpoints for Bucket/Object browsing, metrics, and Key management.
- Ensure robust CORS and Bearer Token authentication for remote dashboard connections.
- Add API contract tests for management endpoints, including CORS and auth behavior for remote dashboards.

## F11: Web Dashboard (Frontend)
**Owner:** [Unassigned]
- Scaffold a new SvelteKit project (TypeScript, TailwindCSS).
- Implement a configuration page to set the Backend URL and Admin Token.
- Build UI components: Metrics Dashboard, Bucket/Object File Browser, and Credentials Manager.

## F12: Terminal Dashboard (TUI)
**Owner:** [Unassigned]
- Scaffold a new TypeScript CLI project using OpenTUI.
- Implement CLI arguments/env vars for `BACKEND_URL` and `ADMIN_TOKEN`.
- Build TUI components: Split-pane file explorer, data tables for access keys, and real-time metric charts.
