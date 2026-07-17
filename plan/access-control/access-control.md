# Access Control

## Status

This document is the **single source of truth** for authorization in
fbs-core. It defines the complete access-control architecture: identity
assumptions, the authorization model, evaluation order, action catalog,
resource and grant model, control-plane APIs, integration boundaries,
security rules, AWS compatibility stance, testing expectations, and
documentation ownership.

It is architectural. It does not prescribe implementation code.

When behavior in code, handlers, migrations, or product docs disagrees
with this document, **this document wins** until it is deliberately
updated. Implementation work implements this model; it does not invent a
parallel one.

Related context (descriptive of surrounding systems, not overrides):

- `docs/architecture.md` — runtime layout and request routing
- `docs/setup-and-authentication.md` — credentials, roles, bootstrap
- `docs/s3-api.md` — S3 protocol surface
- `docs/management-api.md` — Management JSON surface
- `plan/s3-compatibility.md` — comparison with AWS S3 feature set (status labels aligned with this doc)
- `compat/s3-tests/markers.md` — why AWS IAM / policy / STS s3-tests markers stay excluded even when grants ship

---

## 1. Purpose

fbs-core authenticates callers (Bearer tokens, AWS SigV4, optional localhost
dev principal) and attaches a principal to the request. Access control
answers a different question: **may this principal perform this action on
this resource?**

Without a dedicated authorization layer, the product collapses to:

- admins may use every bucket
- non-admins may use only buckets they own

That is insufficient for shared buckets, least-privilege automation keys,
read-only clients, and multi-member use on a single node.

Access control in fbs-core is a deliberate **mini-IAM**:

- fixed action vocabulary
- resource grants (bucket + optional object-key prefix)
- single evaluator and evaluation order
- Management API as the control plane

It is **not** full AWS IAM, full bucket/object ACL parity, or STS.

---

## 2. Goals

1. **Shared bucket access** — non-owners can use a bucket without becoming
   admins or taking ownership.
2. **Least privilege** — read, write, list, and delete are distinct
   capabilities.
3. **Single evaluation path** — every S3 data-plane operation obtains a
   decision from one authorization component; handlers do not invent
   private access rules.
4. **Stable identity** — the existing user (credential principal) is the
   only identity type.
5. **Operable control plane** — admins and bucket owners manage grants
   through the Management API without raw SQL.
6. **Predictable denial** — denied S3 calls use S3-style `AccessDenied`
   (and Management uses JSON `forbidden`) consistent with the rest of the
   product’s error patterns.
7. **Product fit** — single-node, SQLite-backed, no external policy engine
   or directory service.

---

## 3. Out of Scope

These are permanent product boundaries for this architecture, not deferred
work items:

| Area | Rationale |
|---|---|
| Full AWS IAM policy language | Effect/Action/Resource/Condition documents, wildcards, ARNs, and AWS evaluation quirks are a different product |
| STS / AssumeRole / temporary sessions | Identity is long-lived keys only |
| Object ACLs (`?acl` on objects) | Legacy model; poorly suited to this design |
| Bucket ACL protocol (`?acl` on buckets) | Ownership is a first-class field, not an ACL document |
| Separate IAM user/group/role entities | The `users` table is the principal store |
| Cross-account principals | Single deployment, single tenancy |
| Condition keys (IP, time, network, headers, tags) | Evaluator stays deterministic and small |
| Resource tags as policy inputs | No tagging subsystem in this model |
| Anonymous public access via policy | Public reads use signed `/public/...` URLs only |
| Encryption / KMS authorization | No SSE-KMS surface |
| Fine-grained Management RBAC for metrics, global key admin, forced bucket wipe | Those Management capabilities remain admin-only |
| CORS configuration as authorization | Browser CORS is a separate S3 compatibility concern |

S3 `?policy` (bucket policy documents) is **not** part of this
architecture. Normalized grants are the sole authorization data model.
The Management API is the sole grant control plane.

---

## 4. Design Principles

1. **Authentication and authorization are separate.** Authentication
   resolves a principal. Authorization decides allow or deny for an
   action on a resource. Handlers never re-parse credentials to decide
   access.
2. **Deny by default.** Missing grants, unknown actions, and failed
   evaluation paths do not grant access.
3. **Admin is a short-circuit, not a grant row.** System admins have full
   S3 data-plane access and full Management access without per-bucket
   grants.
4. **Owner is a short-circuit, not a grant row.** Bucket ownership means
   full data-plane control of that bucket. Ownership is not simulated as
   synthetic grant rows.
5. **One action vocabulary.** Handlers map protocol operations to named
   actions. Grants and the evaluator speak only those names.
6. **Grants are positive allows only.** There is no explicit-deny grant
   type. Revocation is deactivation or deletion of grants.
7. **Prefix is the only object-key refinement.** Literal key prefixes
   cover folder-style sharing and constrained upload paths without a
   pattern language.
8. **Grants are the source of truth.** There is no second, competing
   policy document evaluator.
9. **No silent privilege expansion.** Creating a key, rotating secrets, or
   renaming a display name does not widen access. Only role changes,
   ownership changes, and grant mutations change authorization.
10. **Fail closed on evaluator failure.** Storage or evaluation errors
    must not produce allow.

---

## 5. Identity Baseline

Access control assumes the existing authentication model.

### 5.1 Principal

An authenticated principal carries at least:

- user id
- display name
- access key id used for the request
- role (`admin` or `member`)
- dev-mode flag when applicable
- SigV4 signed-header metadata when applicable

Inactive users cannot authenticate. Dev mode may inject a synthetic admin
principal and is restricted to localhost binds.

### 5.2 Roles

| Role | Management API | S3 data plane |
|---|---|---|
| `admin` | Full access (including grant administration on any bucket) | Full access to all buckets and objects |
| `member` | No general Management access; may use grant endpoints only where this document allows (own grants read; grant admin only as bucket owner) | Owned buckets (full) and granted buckets/actions/prefixes |

### 5.3 Public signed reads

`GET`/`HEAD` under `/public/{bucket}/{key}` with HMAC query signatures are
**outside** principal-based authorization. They are a separate product
mechanism and must not be modeled as anonymous grants.

---

## 6. Authorization Model

### 6.1 Decision request

Every protected S3 data-plane operation produces a decision request:

| Field | Meaning |
|---|---|
| Principal | Authenticated identity from request context |
| Action | Name from the action catalog |
| Bucket | Bucket name when the operation is bucket-scoped |
| Object key | Object key when object-scoped; empty for pure bucket operations |

The evaluator returns **allow** or **deny**. Handlers map deny to protocol
errors. The evaluator does not write HTTP responses and does not depend on
the HTTP package.

### 6.2 Effect model

Only **Allow** grants exist. The decision is binary:

- **Allow** if any privilege path succeeds
- **Deny** otherwise

There is no explicit-deny statement type and no AWS-style deny-overrides
ladder.

### 6.3 Evaluation order

For an authenticated principal, evaluate in order and stop at the first
allow:

1. **System admin** — principal role is `admin` → allow for all S3
   data-plane actions defined in this document.
2. **Bucket owner** — principal user id equals the bucket’s owner id →
   allow for all actions on that bucket and its objects (except where a
   special case below says otherwise for global operations that do not
   target an existing owned resource).
3. **Matching grant** — at least one active grant matches principal,
   action, bucket, and object key (including prefix rules) → allow.
4. **Default deny**

Authentication failure and missing principal are handled before
authorization. Authorization never runs for unauthenticated callers.

### 6.4 Special cases

**CreateBucket**

- Not grant-based (the bucket does not exist yet).
- Any authenticated active user may create a bucket and becomes its owner.
- Admins may create buckets and own them under the same ownership rules
  as today (including durable owner user materialization in dev mode where
  the product already requires it).

**DeleteBucket**

- Allowed for system admin or bucket owner only.
- **Not grantable.** No grant action authorizes non-owner bucket deletion.
  This prevents tenancy destruction and orphaned ownership via broad
  grants.

**ListBuckets**

Return the union of:

- all buckets, if the principal is admin
- buckets owned by the principal
- buckets on which the principal has **any** active grant (any action)

Seeing a bucket in ListBuckets does not imply `s3:ListBucket` on keys.
Listing object keys still requires owner, admin, or a matching
`s3:ListBucket` grant under the list-prefix rules.

**Missing bucket / existence signaling**

Authorization must not invent a new information-disclosure policy.
Handlers keep the product’s existing not-found vs forbidden patterns for
each operation family.

**Public signed reads**

Outside this evaluator (see §5.3).

---

## 7. Action Catalog

Actions are product-level names, not HTTP methods. Every implemented S3
data-plane operation maps to this catalog. Adding a new S3 operation
requires adding or reusing an action here and wiring the handler through
the evaluator.

### 7.1 Bucket-scoped actions

| Action | Operations |
|---|---|
| `s3:CreateBucket` | Create bucket (`PUT /{bucket}` when creating) |
| `s3:DeleteBucket` | Delete bucket (`DELETE /{bucket}`) |
| `s3:ListBucket` | ListObjects v1/v2, HeadBucket, GetBucketLocation |

Notes:

- `s3:CreateBucket` is a global capability for authenticated users, not a
  per-bucket grant.
- `s3:DeleteBucket` is admin/owner only and never appears as a grantable
  action on grant create/update APIs.
- HeadBucket and GetBucketLocation use `s3:ListBucket` so a list grant is
  sufficient to treat a shared bucket as reachable.

If bucket-level listing of multipart uploads is implemented as an S3
operation, it uses `s3:ListBucket` unless a dedicated action is added to
this catalog in the same change that implements the operation.

### 7.2 Object-scoped actions

| Action | Operations |
|---|---|
| `s3:GetObject` | GetObject, HeadObject; CopyObject **source** |
| `s3:PutObject` | PutObject; CopyObject **destination**; CreateMultipartUpload; UploadPart; CompleteMultipartUpload |
| `s3:DeleteObject` | DeleteObject; each key in DeleteObjects |
| `s3:ListMultipartUploadParts` | ListParts |
| `s3:AbortMultipartUpload` | AbortMultipartUpload |

Multipart write lifecycle is covered by `s3:PutObject`. That keeps “can
write” understandable for automation keys and avoids a parallel multipart
permission family.

### 7.3 CopyObject

Copy is compound:

- source requires `s3:GetObject` (or owner/admin on the source bucket)
- destination requires `s3:PutObject` (or owner/admin on the destination
  bucket)

Both must allow. Same-bucket copies still perform both checks so
prefix-limited writers cannot read outside their prefix by copying.

### 7.4 Multi-object delete

Each key is authorized independently for `s3:DeleteObject`. The response
follows existing multi-delete partial-result semantics: allowed keys are
deleted; denied keys are reported per key where the protocol supports it.

### 7.5 Management capabilities (not S3 actions)

General Management capabilities (metrics, config, global key admin, forced
bucket empty/delete, public URL minting) remain **admin-only**. They are
not expressed as `s3:*` grants.

Grant administration uses the rules in §10, not the S3 action catalog.

---

## 8. Resource Model

### 8.1 Grant target

A grant targets:

- **bucket name** (required)
- **key prefix** (optional; empty means the entire bucket keyspace)

### 8.2 Prefix matching

An object key matches a grant when:

- the grant prefix is empty, or
- the object key equals the prefix, or
- the object key starts with the prefix

Matching is **literal string prefix** on the stored object key. There is
no glob language (`*`, `?`). Trailing `/` is conventional for folder-style
prefixes but is not required by the system.

### 8.3 ListBucket and prefixes

For `s3:ListBucket`:

- A grant with an **empty** prefix authorizes listing for any request
  prefix on that bucket (subject to normal list API limits).
- A grant with a **non-empty** prefix does **not** authorize unrestricted
  whole-bucket listing.
- The request’s list `prefix` query value must itself be covered by at
  least one of the caller’s applicable list grants (or owner/admin).
  “Covered” means the request prefix equals the grant prefix, is empty
  only when the grant prefix is empty, or extends the grant prefix (request
  prefix starts with grant prefix) in the natural nested way: the caller
  may narrow into a subtree they can already list, not widen outside it.

Authorization does not invent a second listing algorithm and does not
require per-key filtering of list results when the request prefix is
already authorized.

### 8.4 Ownership

Ownership is a first-class property of the bucket:

- set at CreateBucket to the creating principal’s user id
- never implied solely by grants
- deleting all grants must not remove owner access

**Ownership transfer** is an explicit Management operation (admin, or
current owner transferring to another active user). It is not a side
effect of grants. After transfer, the previous owner retains access only
if they are admin or hold grants.

### 8.5 Grantees

Grantees are existing users, referenced by **user id**.

Grants must not key off access key id as the durable subject, so key
rotation for the same user does not drop grants. Create APIs may accept an
access key id as a **lookup** that resolves to user id server-side.

Not supported as grantees:

- anonymous `*`
- groups
- role names as principal targets
- external account identifiers

---

## 9. Grant Data Model

SQLite is the system of record. Exact physical column names are
implementation details; the conceptual model is normative.

### 9.1 Grant entity

| Concept | Description |
|---|---|
| Id | Stable unique identifier |
| Bucket name | Target bucket; referential integrity to buckets |
| Grantee user id | Subject of the allow; referential integrity to users |
| Action | Exactly one action from the grantable subset of the catalog |
| Key prefix | Optional object-key prefix; empty string means whole bucket |
| Active | Soft-disable without deletion |
| Created by | User id of the actor who created the grant |
| Created at / updated at | Audit timestamps |
| Note | Optional human label |

### 9.2 Storage shape

**One row per action.** A grant that conveys get and put is two rows
(same grantee, bucket, prefix; different actions). This makes partial
revoke, uniqueness, and indexing straightforward and avoids encoding
action sets as opaque blobs in the security table.

### 9.3 Grantable actions

Grant create/update accepts only:

- `s3:ListBucket`
- `s3:GetObject`
- `s3:PutObject`
- `s3:DeleteObject`
- `s3:ListMultipartUploadParts`
- `s3:AbortMultipartUpload`

Not grantable:

- `s3:CreateBucket` (global authenticated capability)
- `s3:DeleteBucket` (admin/owner only)

### 9.4 Integrity rules

- Deleting a bucket cascades deletion of its grants.
- Deleting a user cascades deletion of grants where that user is grantee.
  `created_by` may be retained for audit or set null; it must not block
  user deletion.
- Creating a grant for an inactive user is rejected.
- Creating a grant on a nonexistent bucket is rejected.
- Unknown or non-grantable actions are rejected at write time.
- Duplicate active grant (same grantee, bucket, action, prefix) is
  **idempotent**: create succeeds and returns the existing grant (or an
  equivalent representation) rather than hard-failing.
- Prefix length and character constraints follow object-key safety rules
  already enforced by the product for keys, applied to the prefix string
  where applicable.

### 9.5 Indexes and access patterns

The store must efficiently support:

- evaluation lookup by bucket + grantee
- list grants by bucket (owner/admin UI)
- list grants by grantee (admin and “my grants”)
- cascade maintenance by bucket and by user

### 9.6 Caching

Incorrect grant caching is a security defect.

- Shared process-wide grant caches are allowed only with correct
  invalidation on create, update, delete, bucket delete, and user delete.
- Request-scoped memoization of evaluation inputs for a single request is
  allowed and preferred when reducing repeated lookups.
- If invalidation cannot be guaranteed, do not cache grants across
  requests.

---

## 10. Control Plane: Management API

The Management API under `/api/management` is the **only** grant control
plane. Responses use the existing JSON error envelope and
`Cache-Control: no-store` conventions.

### 10.1 Capabilities

| Capability | Who |
|---|---|
| List grants for a bucket | Admin, or owner of that bucket |
| Create grant on a bucket | Admin, or owner of that bucket |
| Update grant (prefix, active, note; action changes via replace semantics as implemented consistently) | Admin, or owner of that bucket |
| Delete grant | Admin, or owner of that bucket |
| List grants for a user | Admin |
| List my grants | Authenticated principal, for their own user id only |
| Transfer bucket ownership | Admin, or current owner |

### 10.2 Create semantics

Create accepts:

- grantee identity (user id, or access key id resolved server-side to user
  id)
- one or more grantable actions (materialized as one row per action)
- optional key prefix
- optional note

Responses never include raw secrets. They may include safe grantee
metadata already used elsewhere (display name, access key ids as public
identifiers).

### 10.3 Authorization for grant routes

- **Admin** — any bucket, any user listing of grants.
- **Bucket owner** — grant CRUD only for buckets they own.
- **Member non-owner** — cannot mutate grants; may list only their own
  grants via “my grants.”
- **Inactive principals** — cannot authenticate; no control-plane access.

Grant routes that are owner-capable must not rely solely on global
admin middleware. They use: authenticated, and (admin **or** owner of
target bucket), except “my grants” and admin-only user grant listing.

### 10.4 Activity and audit

Grant create, update, delete, and ownership transfer are recorded in the
activity log with actor, bucket, grantee (when applicable), and action
set or transfer target.

Data-plane denials are not required in the activity log. Operational
logs may record them at debug level to avoid log storms.

### 10.5 Bootstrap and keys

Bootstrap is unchanged: loopback creates the first admin.

Key creation still assigns `admin` or `member`. Grants are attached after
the user exists. The Management API may offer a **transactional convenience**
to create a member key and initial grants together; that is sugar over the
same grant model, not a second permission system.

### 10.6 Public URL minting

Minting signed public object URLs remains **admin-only**. Holding
`s3:GetObject` does not authorize minting world-readable links.

---

## 11. Integration Architecture

### 11.1 Package boundaries

| Component | Responsibility |
|---|---|
| `internal/auth` | Credential authentication → principal in context |
| `internal/authz` | Pure allow/deny evaluation against admin, owner, and grants |
| `internal/metadata` | Persist and query grants, buckets, users |
| `internal/s3` | Map S3 operations to actions/resources; call authz; emit S3 errors |
| `internal/management` | Grant and ownership control-plane handlers; JSON errors |

Normative rules:

- Handlers do not embed authorization SQL.
- Repositories do not emit HTTP.
- `authz` does not depend on `net/http`.
- Credential parsing stays in `auth`; access decisions stay in `authz`.

### 11.2 S3 request flow

1. Router matches the S3 route.
2. Authentication middleware resolves the principal or rejects.
3. Handler determines action(s) and resource (bucket, key).
4. Handler loads bucket metadata when needed (existence, owner).
5. Handler requests an authorization decision.
6. Deny → S3 `AccessDenied` (or the operation family’s existing equivalent).
7. Allow → existing storage/metadata behavior.

Coarse “can touch this bucket at all” checks are replaced by
action-specific authorization. List, create, copy, multipart, and
multi-delete paths all use the same evaluator.

### 11.3 Management grant flow

1. Authenticate.
2. Authorize actor (admin, owner, or self for “my grants”).
3. Validate actions and prefix against this document.
4. Persist via metadata.
5. Return JSON DTOs; write activity entries for mutations.

### 11.4 ListBuckets

- Admin → all buckets.
- Otherwise → owned buckets ∪ buckets with any active grant for the
  principal.

### 11.5 Multipart and system tasks

- User multipart operations use the action mapping in §7.
- Background multipart cleanup, reconciliation, and storage maintenance are
  trusted server tasks. They do not run as end-user principals and do not
  pass through user authorization.

---

## 12. Security Properties

1. **Fail closed** on evaluation or grant-store errors (deny or 5xx; never
   allow).
2. **Grants cannot grant Management admin.** Role elevation remains the
   existing admin role assignment path.
3. **Grants cannot authorize DeleteBucket.**
4. **Owners and admins cannot be locked out** of a bucket solely by grant
   deletion; their short-circuits do not depend on grants.
5. **Prefix is not a filesystem path.** Object key validation remains the
   storage/API layer’s responsibility; authorization assumes accepted keys.
6. **Existence vs authorization signaling** stays consistent with existing
   S3 handler patterns; no ad-hoc per-route disclosure changes.
7. **Copy dual-check** is mandatory (get source, put destination).
8. **Inactive users** never authenticate; grant rows for them do not
   produce access until the user is active again and authenticates.
9. **Dev admin** short-circuits as admin; it must not skip ownership
   materialization rules the product already enforces for owned buckets.
10. **No cookie sessions** or new credential channels; existing Bearer and
    SigV4 rules remain.

---

## 13. Compatibility with AWS S3

| AWS concept | fbs-core stance |
|---|---|
| IAM identity policies | Not used; role + grants |
| Resource-based bucket policies | Not used; Management grants only |
| ACLs | Not implemented |
| Bucket owner full control | Owner short-circuit |
| Access points | Out of scope |
| STS / session policies | Out of scope |
| Public access blocks | Unnecessary; anonymous S3 policy access does not exist |
| Signed public URLs | Separate HMAC mechanism retained |

fbs-core remains intentionally smaller than AWS. S3 clients must use
credentials tied to local users; sharing is done by grants, not by AWS
policy documents.

---

## 14. Testing Requirements

These tests are part of the architecture’s definition of done for the
feature, not optional polish.

### 14.1 Evaluator

Cover at least:

- admin allow on any bucket/action
- owner allow on owned bucket
- owner deny on foreign bucket without grant
- grantee allow for exact action
- grantee deny for wrong action
- prefix match and mismatch
- inactive grant ignored
- create bucket allowed for authenticated member
- delete bucket denied for grantee even with broad object grants
- copy requires get on source and put on destination
- list bucket requires `s3:ListBucket` or owner/admin
- ListBuckets visibility with any grant
- default deny with no grants
- evaluator store error does not allow

### 14.2 S3 HTTP black-box

For list, get, put, delete, multipart, copy, and multi-delete:

- owner succeeds
- stranger denied
- grantee succeeds only for allowed actions and prefixes
- admin succeeds
- deny paths use `AccessDenied` (or established equivalents)

### 14.3 Management

- admin grant CRUD on any bucket
- owner grant CRUD on own bucket
- non-owner member cannot mutate grants
- member can list own grants
- invalid / non-grantable actions rejected
- duplicate grant idempotent
- cascade on bucket delete
- ownership transfer updates owner short-circuit behavior
- activity entries for grant mutations and transfer

### 14.4 Regression

- Bearer, SigV4, dev mode, and inactive-user auth tests remain valid
- Bootstrap and single-admin first-bucket flows remain valid
- Former owner-only suites are updated to the grant model rather than left
  asserting obsolete semantics

---

## 15. Documentation Ownership

While this file is the design source of truth, the following product docs
must describe the **implemented** behavior consistently with it once the
code lands (and must be updated in the same change set as behavior
changes):

| Document | Must cover |
|---|---|
| `docs/architecture.md` | `auth` vs `authz`, evaluation order |
| `docs/setup-and-authentication.md` | Identity and roles; pointer to grants for sharing |
| `docs/management-api.md` | Grant endpoints, ownership transfer, who may call them |
| `docs/s3-api.md` | Action mapping implications, deny behavior, ACL/policy still unsupported |
| `docs/development.md` | How to add actions when new S3 operations are added |
| `plan/s3-compatibility.md` | Access control described as the grants model; AWS IAM/policy/STS marked permanent non-goal |
| `compat/s3-tests/markers.md` | Keep `iam_*` / `bucket_policy` / STS markers dropped; grants progress is not green AWS IAM tests |

---

## 16. Correctness Properties

The system is correct with respect to this architecture when all of the
following hold:

1. A member can receive read-only object GET access (mapped to `s3:GetObject`) or
   bucket-list access (mapped to `s3:ListBucket`) via separate grants, and
   successfully get/list within grant scope without Management admin.
2. A member can receive write access limited to a key prefix and cannot
   read or write outside that prefix.
3. Admins and owners retain full data-plane control without requiring
   grant rows for themselves.
4. Every implemented S3 data-plane operation is mapped through the action
   catalog and evaluator; no residual private “owner-only boolean” path
   remains for normal bucket/object operations.
5. Grant administration and ownership transfer are available through the
   Management API under the rules in §10.
6. There is no second authorization authority (no live ACL or policy
   document evaluator alongside grants).
7. The deployment model remains a single binary with SQLite; no external
   IAM service is required.

---

## 17. Summary

fbs-core access control is:

- **Identity:** existing users and keys (`admin` / `member`)
- **Privilege paths:** admin → bucket owner → active grants → deny
- **Grants:** per-user, per-bucket, per-action rows with optional key prefix
- **Actions:** fixed `s3:*` catalog with multipart under write, compound
  checks for copy, non-grantable bucket delete
- **Control plane:** Management API only
- **Non-goals as product law:** no AWS IAM language, no ACLs, no STS, no
  anonymous policy access, no condition engine

That is the complete authorization architecture for the project.
