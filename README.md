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
- **Bucket Operations:** `LIST` (ListObjectsV2)
- **Multipart Uploads:** `CreateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload`

### Performance & Caching
- **Zero-Copy Transfers:** Utilizes `sendfile()` to stream objects from disk directly to the network.
- **In-Memory Caching:** Go Memory LRU Cache for hot SQLite metadata to prevent disk I/O bottlenecks.
- **SQLite Optimization:** Tuned with `synchronous=NORMAL` and `busy_timeout=5000` for high concurrency.

### Security & Data Integrity
- **Dual Authentication:** AWS SigV4 for standard S3 SDKs, and Bearer Tokens for lightweight management. 
- **Path Sanitization:** Secure handling of object keys to prevent path traversal attacks.
- **Data Consistency:** Writes data to disk first (`.tmp/UUID` \u2192 rename), then inserts metadata into SQLite. Built-in startup reconciliation to purge orphaned data.
- **Upload Checksums:** Real-time validation of `Content-MD5` and `x-amz-checksum-*` headers on upload.

## Project Structure

- `cmd/server/`: Main application entry point.
- `internal/`:
  - `auth/`: Authentication framework (AWS SigV4 & Bearer tokens).
  - `config/`: Application configuration parsing.
  - `http/`: HTTP router and core middleware (logging, recovery).
  - `metadata/`: SQLite database interactions for buckets, objects, and multipart uploads.
  - `s3/`: S3 API handlers and protocol logic.
  - `server/`: Server initialization and lifecycle management.
  - `storage/`: Disk storage engine (read, write, delete, path sanitizing, and reconciliation).
- `migrations/`: SQLite database schemas and migrations.
- `spec/`: Product specifications, features, and architectural flow diagrams.
