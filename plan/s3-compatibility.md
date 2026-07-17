# S3 Compatibility: fbs-core vs AWS S3

## Overview

fbs-core is a minimal S3-compatible storage server intended for local and
small-scale use. It implements the core S3 data-plane operations needed for
standard object storage workflows but omits most of AWS's control-plane,
multi-account security, and scale-out features.

**Related docs**

| Doc | Role |
|-----|------|
| [`plan/access-control/access-control.md`](./access-control/access-control.md) | Authorization architecture (mini-IAM grants) |
| [`compat/s3-tests/markers.md`](../compat/s3-tests/markers.md) | Why ceph/s3-tests markers are dropped or kept |
| [`docs/s3-api.md`](../docs/s3-api.md) | Implemented S3 protocol surface |

Status labels used below:

| Status | Meaning |
|--------|---------|
| **Implemented** | Present in the product today |
| **Planned (grants)** | Designed in access-control; Management grants model (not AWS IAM) |
| **Could be added** | Reasonable product extension; no full design yet |
| **Permanent non-goal** | Product law — will not match AWS shape (see access-control) |
| **Out of scope** | Hard mismatch with single-node / self-hosted design |

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

## Access control (identity & authorization)

Today: authenticate with Bearer tokens or SigV4; authorize with admin role or
bucket ownership. The access-control plan adds **mini-IAM grants** (fixed
actions, per-bucket grants with optional key prefix, Management API control
plane). That is intentional and **not** full AWS IAM.

| Feature | Status | Notes |
|---|---|---|
| Admin / member roles + ownership | Implemented | Owner short-circuit on owned buckets |
| Mini-IAM resource grants (Management API) | Implemented | Fixed actions + optional key prefix; not S3 `?policy` |
| ACLs (`?acl`) | Permanent non-goal | Ownership is first-class; ACL protocol not used |
| Bucket policies (`?policy`) | Permanent non-goal | Grants only; no AWS policy language evaluator |
| AWS IAM users / groups / roles / identity policies | Permanent non-goal | Principals live in local `users` table |
| STS / AssumeRole / session tokens | Permanent non-goal | Long-lived keys only |
| Web identity / OIDC federation | Permanent non-goal | |
| Cross-account principals | Permanent non-goal | Single deployment |
| ABAC / condition keys / tag-based authz | Permanent non-goal | Evaluator stays small and deterministic |
| CORS configuration endpoints (`?cors`) | Could be added | Browser CORS at HTTP layer is separate |

## Data management & storage features

| Feature | Status | Notes |
|---|---|---|
| Versioning (`?versions`) | Could be added | No version IDs or delete markers today |
| Object tags (`?tagging`) | Could be added | Tags for metadata only if added; never authz inputs |
| Lifecycle policies | Could be added | No expiration/transition workers today |
| S3 Object Lock (WORM) | Could be added | Usually couples to versioning |
| Encryption (SSE-S3, SSE-C) | Could be added | Bytes stored as-is today |
| Encryption (SSE-KMS) / KMS authz | Permanent non-goal | No KMS surface in access-control model |
| Storage classes / tiering | Out of scope | Single on-disk store |
| S3 Select (SQL over objects) | Out of scope | Query engine |
| S3 Inventory / batch operations | Could be added | No design yet |
| AppendObject | Out of scope | No plan; Put/Copy/multipart only |

## Other AWS surfaces

| Category | Feature | Status |
|---|---|---|
| Replication | CRR / SRR | Out of scope — single-node |
| Notifications | SNS / SQS / Lambda event notifications | Could be added |
| Website hosting | Static website config (`?website`) | Could be added |
| Logging | Server access logging | Could be added |
| Auth protocol | Signature Version 2 | Permanent non-goal — SigV4 (+ Bearer) only |
| Advanced | Transfer Acceleration, multi-region APs, Outposts | Out of scope — single-node |
| Advanced | S3 Object Lambda | Out of scope — request transform pipeline |
| Advanced | S3 Access Points / S3 Control | Out of scope — AWS control-plane primitives |
| Advanced | Requester Pays | Out of scope — no billing model |
| Cloud tier | Cloud transition / restore | Out of scope — local disk only |

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

**AWS S3** has provided strong read-after-write consistency for all object
operations (PUT, POST, DELETE, overwrite, GET, HEAD, LIST, multipart)
since December 2020 in all commercial regions. Bucket-configuration
changes (e.g., enabling versioning, modifying bucket policies) remain
eventually consistent.

**fbs-core** is **strongly consistent** for all operations — SQLite is the
source of truth, and the write path uses an atomic rename + metadata upsert
pattern. Reads see committed data immediately.

### Auth & Access Control

**AWS S3** uses IAM policies attached to users, groups, roles, and
resource-based bucket policies. Access is governed by statements with
`Effect`, `Action`, `Resource`, and `Condition` blocks. STS provides
temporary credentials via AssumeRole, web identity, or SAML federation.

**fbs-core today** uses a two-tier role system (`admin` / `member`) with a
bucket ownership check:

| Role | Management API | S3 buckets |
|---|---|---|
| admin | Full access | All buckets |
| member | No access | Owned buckets only |

**fbs-core direction** (access-control plan): same identity model, plus
**resource grants** so non-owners can get least-privilege data-plane access
without becoming admins. Grants are positive allows only; there is no second
AWS-style policy document evaluator.

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
with a lightweight management API and a deliberately small authorization
model (roles, ownership, then grants).

AWS control-plane features — IAM policy language, STS, bucket policy
documents, multi-account — are permanent non-goals. Some data-management
features (versioning, lifecycle, object lock, optional SSE) may be added
later; until then they remain compatibility gaps, not promises.

## Compatibility testing

Use `compat/s3-tests/` for AWS-like gap discovery. The default `--core`
filter drops only tests explicitly marked as permanent non-goals or large
deferred clusters in the markers file; unmarked upstream tests still run
and may produce failures. `--core` is not a complete exclusion or a
guaranteed-green compatibility gate — see
[`compat/s3-tests/markers.md`](../compat/s3-tests/markers.md).

Claimed behavior is enforced by `go test ./...`, not by s3-tests CI gates.

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
- Compliance requirements that need AWS-native encryption, object lock, and
  audit logging
- Any need for full IAM policy language, STS, or the advanced features listed
  as permanent non-goal / out of scope above
