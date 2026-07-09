# S3 Compatibility: fbs-core vs AWS S3

## Overview

fbs-core is a minimal S3-compatible storage server intended for local and
small-scale use. It implements the core S3 data-plane operations needed for
standard object storage workflows but omits most of AWS's control-plane,
security, and scale-out features.

## Implemented S3 Features

| Feature | Notes |
|---|---|
| ListBuckets | |
| CreateBucket / HeadBucket / DeleteBucket | Delete on empty buckets only |
| GetBucketLocation | Config-defined single region |
| ListObjectsV1 / V2 | Prefix, delimiter, start-after, max-keys |
| PutObject / GetObject / HeadObject / DeleteObject | |
| DeleteObjects (multi-delete) | |  
| CopyObject | |
| Multipart upload | Initiate, upload parts, complete, abort, list parts |
| SigV4 header auth | |
| SigV4 query-string auth | |
| Presigned GET URLs (public read) | Via management API; requires `FBS_PUBLIC_READ_SIGNING_SECRET` |
| ETags | |

## Not Implemented vs Out of Scope

| Category | Feature | Status |
|---|---|---|
| Access control | ACLs (`?acl`) | Could be added |
| Access control | Bucket policies (`?policy`) | Could be added |
| Access control | CORS configuration endpoints (`?cors`) | Could be added |
| Data management | Versioning (`?versions`) | Could be added |
| Data management | Object tags (`?tagging`) | Could be added |
| Data management | Lifecycle policies | Could be added |
| Data management | S3 Object Lock (WORM) | Could be added |
| Data management | S3 Inventory | Could be added |
| Data management | S3 Select (SQL over objects) | Out of scope — query engine |
| Data management | Batch operations | Could be added |
| Replication | CRR / SRR | Out of scope — single-node |
| Encryption | SSE-S3, SSE-KMS, SSE-C | Could be added |
| Notifications | SNS / SQS / Lambda event notifications | Could be added |
| Website hosting | Static website config (`?website`) | Could be added |
| Auth / STS | Security Token Service (AssumeRole, GetSessionToken) | Could be added |
| Auth / STS | Web identity federation | Could be added |
| Advanced features | Transfer Acceleration | Out of scope — single-node |
| Advanced features | S3 Object Lambda | Out of scope — request transform pipeline |
| Advanced features | Multi-Region Access Points | Out of scope — single-node |
| Advanced features | S3 Access Points | Out of scope — AWS networking primitive |
| Advanced features | Requester Pays | Out of scope — no billing model |
| Advanced features | Request route / Outposts | Out of scope — single-node |

## Key Architectural Differences

### Storage Model

**AWS S3** is a globally distributed, multi-region object store that
replicates data across multiple availability zones with 11 9s of durability.
Objects are addressed by URL, stored in a flat namespace per bucket, and
backed by a custom distributed filesystem.

**fbs-core** stores every object as an ordinary file on a single machine's
disk under a configurable `--data-dir`. Object keys are hashed to UUID-based
paths to avoid path-length issues and races during overwrite. There is no
replication — everything lives on one disk.

### Consistency

**AWS S3** provides read-after-write strong consistency for PUTs of new
objects (most regions as of late 2020), with eventual consistency for some
overwrite and delete operations in legacy scenarios.

**fbs-core** is **strongly consistent** for all operations — SQLite is the
source of truth, and the write path uses an atomic rename + metadata upsert
pattern. Reads see committed data immediately.

### Auth & Access Control

**AWS S3** uses IAM policies attached to users, groups, roles, and
resource-based bucket policies. Access is governed by statements with
`Effect`, `Action`, `Resource`, and `Condition` blocks. STS provides
temporary credentials via AssumeRole, web identity, or SAML federation.

**fbs-core** uses a two-tier role system (`admin` / `member`) with a
bucket ownership check:

| Role | Management API | S3 buckets |
|---|---|---|
| admin | Full access | All buckets |
| member | No access | Owned buckets only |

Bearer tokens (`fbsa_...`) are an fbs-core extension — AWS S3 only
supports SigV4 (plus STS session tokens).

### Encryption

AWS S3 provides server-side encryption options (SSE-S3, SSE-KMS, SSE-C)
and client-side encryption. fbs-core stores bytes as-is on disk — whatever
the filesystem provides is what you get.

### Operational Model

| | AWS S3 | fbs-core |
|---|---|---|
| Dependencies | DynamoDB, KMS, load balancers, edge locations | A single Go binary + SQLite + a data directory |
| Scaling | Multi-region, auto-scaling | Single node |
| Regions | Global region/az model | Single config string |
| Billing | Per-request, per-byte-metered | Out of scope — self-hosted |
| Durability | 11 nines | Filesystem-dependent |
| Metadata | Internal distributed KV store | SQLite with optional in-memory LRU cache |

## Design Intent

fbs-core is not trying to be a full S3 replacement. The scope is focused on
S3 data-plane operations (bucket and object CRUD, multipart, listing, copy)
with a lightweight management API. The control-plane features — policies,
versioning, replication, encryption, notifications — are absent by design.

## When to Pick fbs-core Over AWS S3

- Local development / testing where you want real S3 API semantics without
  cloud dependencies
- Small-scale internal storage (single team, single machine)
- Air-gapped or offline environments
- Learning or experimenting with S3 API patterns
- CI pipelines where spinning up MinIO is overkill

## When AWS S3 Is the Right Choice

- Production workloads needing durability, replication, and scale
- Multi-region or multi-tenant scenarios
- Compliance requirements (encryption, object lock, audit logging)
- Any need for the advanced features listed above
