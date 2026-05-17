# Product Specification: Lightweight S3-Compatible Storage Ecosystem

## 1. Overview
This system is a self-hosted, lightweight object storage ecosystem. It provides an AWS S3-compatible API for client applications, optimized for single-node deployments (homelabs, small to medium projects). The ecosystem consists of a high-performance core storage engine and two remote management dashboards (Web and Terminal).

## 2. System Architecture (Decoupled)

### 2.1 Core Backend (Go + SQLite)
- **Role:** Handles all S3-compatible data operations, disk I/O, and exposes the Management API.
- **Stack:** Go, `chi` router, SQLite (WAL mode).
- **Storage:** Local File System with opaque per-object backing paths under each bucket directory. Object keys are stored in SQLite metadata, not used as final filenames. Keys are still validated to prevent traversal attacks and unsafe characters.
- **Performance:** OS Page Cache (`sendfile`), Go Memory LRU Cache for hot SQLite rows. SQLite tuned with `synchronous=NORMAL` and `busy_timeout=5000`.
- **Authentication:** Dual Mode (AWS SigV4 for SDKs, Bearer Token for homelab/simple, `--dev` flag to bypass). Bearer token secrets stored as SHA-256 hashes, never in plaintext. SigV4 secret keys are stored raw (required for HMAC signature verification) and presented once to the user at creation time.

### 2.2 Web Dashboard (SvelteKit)
- **Role:** A standalone graphical interface for administrators to manage buckets, keys, and view metrics in the browser.
- **Stack:** SvelteKit, TypeScript, TailwindCSS.
- **Connectivity:** Connects to the Core Backend's Management API via user-configured URL and Bearer Token.

### 2.3 Terminal Dashboard (OpenTUI)
- **Role:** A standalone CLI interface for system administrators who prefer keyboard-driven terminal management.
- **Stack:** TypeScript, Node/Bun, OpenTUI.
- **Connectivity:** Connects to the Core Backend's Management API via user-configured URL and Bearer Token.

## 3. Core S3 API Capabilities
- **Object Operations:** `PUT`, `GET`, `DELETE`, `HEAD`
- **Bucket Operations:** `CREATE` (CreateBucket), `LIST` (ListObjectsV2)
- **Multipart Uploads:** `CreateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload`

## 3.1 Testing & Compatibility Strategy
- The system targets AWS S3-compatible behavior, validated primarily through black-box HTTP and API tests.
- Test scenarios may be inspired by established S3-compatible servers, but tests in this repository must be written originally and not copied verbatim from third-party projects.
- Preference is given to protocol-level compatibility checks such as status codes, headers, XML bodies, ETag behavior, range requests, multipart flows, and auth flows over implementation-coupled tests.
- External compatibility suites and AWS CLI or SDK smoke tests may be run in CI or local validation as separate tools, without vendoring third-party AGPL test code into this repository.

## 4. Data Integrity & Consistency
- **Write Order:** Data is written to disk first (`.tmp/UUID` → rename), metadata inserted into SQLite second. SQLite is the single source of truth — if a row doesn't exist, the object doesn't exist.
- **Reserved Paths:** `/public/{bucket}/{key}` is reserved for signed public read URLs, so `public` is not accepted as an S3 bucket name.
- **Startup Reconciliation:** On server boot, purge incomplete files from `.tmp/` and remove orphaned data files that have no matching SQLite metadata row.
- **Upload Checksums:** `Content-MD5` and `x-amz-checksum-*` headers are validated on upload using a `TeeReader` hash pipeline. Mismatches reject the upload before metadata is committed.
- **Stale Multipart Cleanup:** A background goroutine periodically purges multipart uploads older than a configurable TTL (default 24h), reclaiming disk space from abandoned uploads.

## 5. Security
- **Path Sanitization:** Object keys are validated and resolved against the data directory root. Keys containing path traversal sequences (`../`), null bytes, or characters invalid on the host filesystem are rejected. Intermediate directories are created as needed for keys containing `/`.
- **Token Storage:** Bearer tokens are hashed with SHA-256 before storage in SQLite. Raw tokens are returned to the user once at creation time and never stored.
- **Authentication Bypass:** The `--dev` flag is restricted to `localhost` binding only and logs a startup warning.

## 6. Egress & Caching Strategy
- **Browser / CF Cache:** Support for standard HTTP caching headers (`Cache-Control`, `ETag`).
- **Metadata LRU Cache:** Hot metadata is kept in memory to prevent disk I/O bottlenecks.
- **OS Page Cache:** Zero-copy transfers utilizing `sendfile()` to stream objects from disk directly to network. The S3 `GET` path uses a minimal middleware chain (no response writer wrapping) to preserve the `io.ReaderFrom` interface required for `sendfile()`. `http.ServeContent()` is used to handle range requests, ETag, and Last-Modified in one call.
