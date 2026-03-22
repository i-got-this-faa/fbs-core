# Features & Delegation Breakdown

This document defines the discrete features required to build the S3-Compatible Storage Ecosystem. Each feature is scoped to be delegatable to a single developer or a small pair-programming team.

## F1: Core HTTP Server & Routing (Backend)
**Owner:** [Unassigned]
- Set up the Go project structure.
- Implement the HTTP router using `chi`.
- Configure dual endpoints (`localhost:9000` and ingress config).
- Add foundational middleware (logging, panic recovery, CORS).

## F2: SQLite Metadata Layer (Backend)
**Owner:** [Unassigned]
- Design the SQLite schema (tables: `users`, `buckets`, `objects`, `multipart_uploads`).
- Configure SQLite for high concurrency (WAL mode, busy timeouts).
- Create Go repository interfaces for CRUD operations on metadata.

## F3: Disk Storage Engine (Backend)
**Owner:** [Unassigned]
- Implement file system operations matching the `/data/{bucket_name}/{object_key}` structure.
- Implement Atomic Writes: upload to `.tmp/UUID`, then rename on success.
- Implement direct file reads and soft delete.

## F4: Authentication Framework & Bearer Tokens (Backend)
**Owner:** [Unassigned]
- Implement the `--dev` CLI flag to bypass auth.
- Implement Bearer Token authentication against the `users` SQLite table.
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

## F7: S3 API - Bucket Operations (Backend)
**Owner:** [Unassigned]
- Implement `ListObjectsV2` endpoint.
- Query the SQLite metadata layer to return paginated directory structures.

## F8: S3 API - Multipart Uploads (Backend)
**Owner:** [Unassigned]
- Implement endpoints: `CreateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload`, `AbortMultipartUpload`.
- Manage temporary parts on disk and track state in SQLite.

## F9: Egress & Caching Optimizations (Backend)
**Owner:** [Unassigned]
- Implement Go Memory LRU cache for hot SQLite metadata queries.
- Add standard CDN headers (`Cache-Control`, `ETag`).
- Optimize the `GET` path to use `sendfile()` (zero-copy).

## F10: Management API (Backend)
**Owner:** [Unassigned]
- Expose RESTful JSON endpoints for Bucket/Object browsing, metrics, and Key management.
- Ensure robust CORS and Bearer Token authentication for remote dashboard connections.

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
