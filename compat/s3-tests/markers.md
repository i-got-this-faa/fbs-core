# Core s3-tests marker filter

This document explains the default pytest marker expression in
[`markers.core`](./markers.core): what we drop from the **core** run, what we
keep, and why.

**Related product docs**

| Doc | Role |
|-----|------|
| [`plan/s3-compatibility.md`](../../plan/s3-compatibility.md) | Implemented surface vs gaps |
| [`plan/access-control/access-control.md`](../../plan/access-control/access-control.md) | Mini-IAM grants architecture (authz source of truth) |
| [`docs/s3-api.md`](../../docs/s3-api.md) | Claimed S3 protocol behavior |

## How the filter works

- `run.sh` (default `--core`) builds a pytest `-m` expression from
  `markers.core`.
- Lines starting with `#` and blank lines are ignored.
- Remaining lines are joined with ` and ` (every clause must match).
- Use `./compat/s3-tests/run.sh --full` to run without this filter.

The filter only affects **marked** upstream tests. Many ceph/s3-tests cases are
unmarked and still run; they may fail when a feature is missing. That is useful
discovery, not a CI gate. Claimed product behavior stays covered by
`go test ./...`.

## Keep vs drop (policy)

Markers are labeled with one of:

| Label | Meaning |
|-------|---------|
| **Noise** | Upstream known-bad or env-specific; not our gap tracker |
| **Permanent non-goal** | Product law (especially access-control architecture). Do not expect AWS parity |
| **Out of scope** | Hard mismatch with single-node / no external services design |
| **Deferred** | Not implemented today; `plan/s3-compatibility.md` may say “Could be added”. Revisit markers when the feature ships |

When a **Deferred** feature lands:

1. Implement and cover with `go test`.
2. Remove the corresponding `not …` line(s) from `markers.core`.
3. Update this file and the checklist narrative in `README.md`.

**Mini-IAM is not AWS IAM.** Shipping grants via the Management API does **not**
mean re-enabling `iam_*`, `bucket_policy`, or STS markers. Those exercise AWS
control-plane protocol surfaces we deliberately do not implement. Progress is
measured with Management grant tests and S3 black-box authz, not green
`test_iam_*` results.

---

## What the core run keeps

Roughly: **data-plane** bucket and object CRUD, listing, copy, multipart,
checksums, and SigV4 auth behavior that matches what fbs-core claims today.

Examples of areas that remain in the default suite (when upstream marks them
or leaves them unmarked):

- ListBuckets, Create/Head/DeleteBucket, GetBucketLocation
- Put/Get/Head/DeleteObject, multi-delete, CopyObject
- ListObjects v1/v2 (prefix, delimiter, pagination)
- Multipart initiate / upload part / complete / abort / list parts
- Basic auth and error-shape cases that do not require IAM APIs

Not every kept test will pass today (e.g. unmarked ACL or CORS probes). Failures
are expected for gaps; the marker filter only removes the large, clearly
out-of-scope clusters.

---

## Dropped markers (full list)

### Noise

| Marker | Why dropped |
|--------|-------------|
| `fails_on_aws` | Upstream marks tests that also fail against real AWS. Not a useful fbs-core signal. |

### Data management (Deferred unless noted)

| Marker | Why dropped | Status |
|--------|-------------|--------|
| `versioning` | No `?versioning`, version IDs, or version history. Overwrite replaces. | Deferred (“Could be added”) |
| `delete_marker` | Delete markers only exist under versioning. Delete is a hard remove. | Deferred (with versioning) |
| `lifecycle` | No lifecycle configuration API or background expiration/transition jobs. | Deferred |
| `lifecycle_expiration` | Expiration rules and worker not implemented. | Deferred |
| `lifecycle_transition` | No storage-class / tier transitions. | Deferred |
| `object_lock` | No WORM, retention, or legal hold. Usually couples to versioning. | Deferred |
| `tagging` | No object/bucket `?tagging` API. Object tags may be added later; tags are **not** used for authorization (see access-control non-goals). | Deferred (object tags); permanent non-goal as ABAC input |
| `storage_class` | Single on-disk store; no STANDARD/IA/Glacier-style classes. | Out of scope (single-node model) |
| `appendobject` | No AppendObject-style API; only Put / Copy / multipart. | Out of scope / no plan |
| `s3select` | Would need a SQL query engine over objects. | Out of scope |

### Access control & AWS IAM protocol (Permanent non-goal)

fbs-core is building a **mini-IAM** (fixed actions + resource grants on the
Management API). That is intentionally **not** AWS IAM language, S3 bucket
policies, ACLs, STS, or multi-account IAM. See
`plan/access-control/access-control.md` §3 and §13.

| Marker | Why dropped |
|--------|-------------|
| `bucket_policy` | No S3 `?policy` documents. Sharing is grants, not resource policies. |
| `object_ownership` | No AWS Object Ownership / ACL-ownership modes. Ownership is a first-class bucket field. |
| `user_policy` | No IAM identity policies on users. |
| `role_policy` | No IAM roles or role policies. |
| `group_policy` | No IAM groups. |
| `session_policy` | No STS session policies. |
| `iam_role` | No IAM role entities; principals are rows in `users`. |
| `iam_user` | No AWS IAM user API; local users/keys only. |
| `iam_account` | No multi-account IAM account model. |
| `iam_cross_account` | Single deployment, single tenancy. |
| `iam_tenant` | No Ceph/RGW-style tenant IAM surface. |
| `test_of_sts` | No STS (AssumeRole, GetSessionToken, temporary creds). Long-lived keys only. |
| `webidentity_test` | No web identity / OIDC federation. |
| `abac_test` | No attribute-based access (condition keys, tag-based authz). |

### Encryption (Deferred product; permanent non-goal as KMS authz surface)

| Marker | Why dropped | Status |
|--------|-------------|--------|
| `encryption` | General SSE suite; bytes stored as-is on disk today. | Deferred for SSE-S3/SSE-C style; SSE-KMS authz out of scope per access-control |
| `sse_s3` | No SSE-S3 default encryption. | Deferred |
| `bucket_encryption` | No bucket default encryption config API. | Deferred |

### Website hosting (Deferred / not planned for core)

| Marker | Why dropped |
|--------|-------------|
| `s3website` | No static website hosting config (`?website`). |
| `s3website_routing_rules` | No routing rules. |
| `s3website_redirect_location` | No website redirect location semantics. |

Website is listed as “Could be added” in the compatibility plan; it is not part
of the core data-plane claim. Keep dropped until a deliberate product decision.

### Logging & notifications

| Marker | Why dropped | Status |
|--------|-------------|--------|
| `bucket_logging` | No server access logging to a target bucket. | Deferred / low priority |
| `bucket_logging_cleanup` | Cleanup fixtures for logging. | Same as logging |
| `fails_without_logging_rollover` | Needs log rollover behavior we do not implement. | Noise + logging gap |
| `sns` | No SNS/SQS/Lambda event notifications. | Deferred (“Could be added”) in compat plan; no design yet |

### Cloud / control-plane AWS services

| Marker | Why dropped | Status |
|--------|-------------|--------|
| `cloud_transition` | Remote/cloud tiering (archive off-node). | Out of scope — single-node disk |
| `cloud_restore` | Restore from cloud tier. | Out of scope |
| `s3control` | S3 Control API (account-level ops, access points, batch, …). | Out of scope |

### Auth protocol

| Marker | Why dropped | Status |
|--------|-------------|--------|
| `auth_aws2` | Signature Version 2 is legacy. fbs-core supports SigV4 (+ Bearer tokens). | Permanent non-goal |

---

## What is *not* filtered (and why that can still fail)

These often still appear in core runs because they are unmarked or only partly
covered by the markers above:

| Area | Notes |
|------|--------|
| ACLs (`?acl`) | Permanent non-goal in access-control design; many tests unmarked → may fail. Prefer dedicated ACL markers if upstream adds them; do not treat failures as mini-IAM progress. |
| CORS | Product may support browser CORS at the HTTP layer; S3 `?cors` config endpoints are a separate gap. |
| Conditional requests, ranges, encoding | Often in-scope; failures are real bugs or edge-case work. |
| Public access blocks | Unnecessary if anonymous policy access does not exist; tests may still appear. |

When mini-IAM grants land, **do not** remove IAM/policy markers solely to
“show green.” Add Management + S3 authz coverage under `go test` instead.

---

## Maintenance checklist

After changing product scope:

1. Update `plan/s3-compatibility.md` (and access-control if authz-related).
2. Edit `markers.core` (one clause per line).
3. Update the tables in this file.
4. Re-run `./compat/s3-tests/run.sh` and refresh `results/checklist.*` if the
   team tracks a shared baseline.
