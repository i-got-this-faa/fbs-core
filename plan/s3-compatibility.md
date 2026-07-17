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

| Feature | Test pass rate | Notes |
|---|---|---|
| ListBuckets | not tested | Covered by Go unit tests |
| CreateBucket / HeadBucket / DeleteBucket | 78% (14/18) | `test_bucket_create_exists`, `test_bucket_get_location`, `test_bucket_recreate_not_overriding`, `test_bucket_create_special_key_names` fail |
| GetBucketLocation | **0% (0/1)** | `test_bucket_get_location` fails — see above |
| ListObjectsV1 / V2 | 81% (67/83) | Strong coverage. Failures in encoding, anonymous access, some prefix+delimiter edge cases |
| PutObject / GetObject / HeadObject / DeleteObject | 46% (6/13) | CORS presigned puts fail. Cache-Control, Expires headers not forwarded. Bucket-gone edge case fails |
| DeleteObjects (multi-delete) | tested within Put/Delete | Two tests pass (key limit, basic) |
| CopyObject | 50% (2/4) | Conditional copy edge cases fail (`if-match`, `if-none-match`) |
| Multipart upload | 56% (23/41) | Core flow works. Checksum-related uploads, object attributes, conditional puts, and `test_multipart_get_part` fail |
| SigV4 header auth | not directly tested | Covered by Go unit tests |
| SigV4 query-string auth | tested via presigned | |
| Presigned URLs (public read) | not tested | Covered by Go unit tests |
| ETags | tested within Put/Delete | `test_object_write_check_etag` passes |
| Conditional requests (GET) | 45% (5/11) | GET if-match/if-modified-since works. PUT if-match/if-none-match all fail |
| Object Attributes (`?attributes`) | **0% (0/2)** | `test_get_object_attributes` and `test_get_object_torrent` fail |
| Checksum headers (SHA256, CRC32, etc.) | **0% (0/4)** | `test_object_checksum_crc64nvme`, `test_object_checksum_sha256`, `test_get_checksum_object_attributes`, `test_post_object_upload_checksum` fail |


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

### Latest test results (2026-07-17)

Run with `bash compat/s3-tests/run.sh --core` on commit `00e7f20`
(staging/multipart merged into main). 441 tests selected, 446 deselected
by markers. Full checklist: [`compat/s3-tests/results/checklist.md`](../compat/s3-tests/results/checklist.md)
and [`compat/s3-tests/results/checklist.html`](../compat/s3-tests/results/checklist.html).

| Feature area | Pass | Fail | Rate |
|---|---:|---:|---:|
| Bucket Ops | 14 | 4 | 78% |
| Conditional Requests | 5 | 6 | 45% |
| Object Attributes / Torrent | 0 | 2 | 0% |
| Put / Delete Object | 6 | 7 | 46% |
| Get / Head / Range | 6 | 9 | 40% |
| List Objects | 67 | 16 | 81% |
| Multipart Upload | 23 | 18 | 56% |
| Copy Object | 2 | 2 | 50% |
| Checksums | 0 | 4 | 0% |
| Headers / Auth edge cases | 17 | 10 | 63% |
| ACL / Public Access | 4 | 37 | 10% |
| Bucket Policy | 0 | 7 | 0% |
| Versioning | 0 | 32 | 0% |
| Object Lock / WORM | 6 | 31 | 16% |
| Utils / Misc | 1 | 0 | 100% |
| Other / Uncategorized | 36 | 69 | 34% |
| **Total** | **187** | **254** | **42%** |

Expected failures come from deferred features (versioning, object lock), permanent non-goals
(ACL, bucket policy, IAM), and known gaps (conditional PUTs, object attributes, checksum header
end-to-end, some bucket-edge cases, CORS presigned URLs).
Uncategorized failures are split between unmarked upstream tests in deferred feature areas
and genuine edge cases not yet handled.

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
