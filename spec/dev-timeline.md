# Development Timeline

This timeline assumes 1-week phases (sprints) across dedicated sub-teams (Backend, Frontend, CLI).

## Phase 1: Backend Foundation (Week 1)
**Goal:** Establish the core infrastructure, database, and ability to write/read raw files.
- **Backend Team:** F1 (Core HTTP Server & Routing)
- **Backend Team:** F2 (SQLite Metadata Layer)
- **Backend Team:** F3 (Disk Storage Engine)
- *Milestone:* Server boots, foundational router tests pass, connects to SQLite, and unit tests can write atomic files to disk.

## Phase 2: MVP & Basic S3 (Week 2)
**Goal:** Basic authentication is in place, and simple `PUT`/`GET` S3 operations work.
- **Backend Team:** F4 (Auth Framework & Bearer Tokens)
- **Backend Team:** F6 (Basic Object Ops - PUT, GET, DELETE, HEAD)
- *Milestone:* A client can authenticate with a Bearer token and upload/download a simple file; basic black-box S3 compatibility tests pass.

## Phase 3: S3 Compliance & Management API (Week 3)
**Goal:** Make the API strictly compatible with standard AWS SDKs and lock in the Management API contract.
- **Backend Team:** F5 (AWS SigV4 Authentication)
- **Backend Team:** F7 (Bucket Operations - ListObjectsV2)
- **Backend Team:** F10 (Management API)
- *Milestone:* `aws s3 ls` works against the server, Management API endpoints are ready for UI consumption, and protocol-level compatibility checks cover the implemented S3 surface.

## Phase 4: Dashboards & Advanced Storage (Week 4)
**Goal:** Handle large files, optimize egress, and build the independent client applications.
- **Backend Team:** F8 (Multipart Uploads), F9 (Egress & Caching Optimizations)
- **Frontend Team:** F11 (SvelteKit Web Dashboard)
- **CLI Team:** F12 (OpenTUI Terminal Dashboard)
- *Milestone:* Both dashboards can successfully connect to the remote Go backend and perform CRUD operations. Can upload 50GB files reliably.

## Phase 5: Integration, Polish & Release (Week 5)
**Goal:** End-to-end testing across all three separate repositories/projects.
- **All Teams:** Fix cross-boundary bugs (e.g., CORS issues, token expiration, UI edge cases).
- **All Teams:** Documentation and deployment guides.
- *Milestone:* Production-ready v1.0 release.
