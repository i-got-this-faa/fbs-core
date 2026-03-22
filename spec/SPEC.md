# Product Specification: Lightweight S3-Compatible Storage Ecosystem

## 1. Overview
This system is a self-hosted, lightweight object storage ecosystem. It provides an AWS S3-compatible API for client applications, optimized for single-node deployments (homelabs, small to medium projects). The ecosystem consists of a high-performance core storage engine and two remote management dashboards (Web and Terminal).

## 2. System Architecture (Decoupled)

### 2.1 Core Backend (Go + SQLite)
- **Role:** Handles all S3-compatible data operations, disk I/O, and exposes the Management API.
- **Stack:** Go, `chi` router, SQLite (WAL mode).
- **Storage:** Local File System (`/data/{bucket_name}/{object_key}`).
- **Performance:** OS Page Cache (`sendfile`), Go Memory LRU Cache for hot SQLite rows.
- **Authentication:** Dual Mode (AWS SigV4 for SDKs, Bearer Token for homelab/simple, `--dev` flag to bypass).

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
- **Bucket Operations:** `LIST` (ListObjectsV2)
- **Multipart Uploads:** `CreateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload`

## 4. Egress & Caching Strategy
- **Browser / CF Cache:** Support for standard HTTP caching headers (`Cache-Control`, `ETag`).
- **Metadata LRU Cache:** Hot metadata is kept in memory to prevent disk I/O bottlenecks.
- **OS Page Cache:** Zero-copy transfers utilizing `sendfile()` to stream objects from disk directly to network.
