# fbs-core

**fbs-core** is a self-hosted, lightweight object storage ecosystem designed for single-node deployments like homelabs or small to medium projects. It provides a high-performance **AWS S3-compatible API**, acting as the core backend storage engine.

## Overview

The `fbs-core` backend handles all S3-compatible data operations, disk I/O, and exposes a Management API for external dashboards (such as a Web Dashboard or Terminal Dashboard). 

- **Language:** Go
- **Storage Metadata:** SQLite (WAL mode)
- **Object Storage:** Local File System (Zero-copy transfers utilizing OS Page Cache `sendfile`)
- **Routing:** `chi` router

## Features

### Core S3 API Capabilities
- **Object Operations:** `PUT`, `GET`, `DELETE`, `HEAD`
- **Bucket Operations:** `CREATE` (CreateBucket), `LIST` (ListObjectsV2)
- **Multipart Uploads:** `CreateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload`

### Performance & Caching
- **Zero-Copy Transfers:** Utilizes `sendfile()` to stream objects from disk directly to the network.
- **In-Memory Caching:** Go Memory LRU Cache for hot SQLite metadata to prevent disk I/O bottlenecks.
- **SQLite Optimization:** Tuned with `synchronous=NORMAL` and `busy_timeout=5000` for high concurrency.

### Security & Data Integrity
- **Unified Authentication:** Protected S3 and Management API routes accept both AWS SigV4 and Bearer tokens. Authorization still depends on the stored user role.
- **First-Start Bootstrap:** On an empty database, create the initial admin once from localhost with `POST /api/setup/bootstrap`.
- **Path Sanitization:** Secure handling of object keys to prevent path traversal attacks.
- **Data Consistency:** Writes data to disk first (`.tmp/UUID` \u2192 rename), then inserts metadata into SQLite. Built-in startup reconciliation to purge orphaned data.
- **Upload Checksums:** Real-time validation of `Content-MD5` and `x-amz-checksum-*` headers on upload.

## First Run

When the database has no users, call the loopback-only bootstrap endpoint from the server host:

```bash
curl -X POST http://127.0.0.1:9000/api/setup/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"Admin User"}'
```

The response includes the initial admin Bearer token and SigV4 access key/secret once. Store them immediately; secrets are not returned by later management reads. `GET /api/setup/status` reports whether bootstrap is still required.

Management and S3 protected routes accept either `Authorization: Bearer ...` or AWS SigV4. SigV4 uses service scope `s3` and region `us-east-1`.

## Project Structure

- `cmd/server/`: Main application entry point.
- `internal/`:
  - `auth/`: Authentication framework (AWS SigV4 & Bearer tokens).
  - `config/`: Application configuration parsing.
  - `http/`: HTTP router and core middleware (logging, recovery).
  - `metadata/`: SQLite database interactions for buckets, objects, and multipart uploads.
  - `setup/`: Loopback-only first-start bootstrap endpoints.
  - `s3/`: S3 API handlers and protocol logic.
  - `server/`: Server initialization and lifecycle management.
  - `storage/`: Disk storage engine (read, write, delete, path sanitizing, and reconciliation).
- `migrations/`: SQLite database schemas and migrations.
- `spec/`: Product specifications, features, and architectural flow diagrams.
