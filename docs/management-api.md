# Management API

The Management API is a JSON API under `/api/management`. Most routes require authentication and the `admin` role. Grant administration and ownership transfer also allow the **bucket owner**. Listing one's own grants (`GET /grants/me`) requires only authentication. Responses use `Cache-Control: no-store`.

Errors use this shape:

```json
{
  "error": {
    "code": "invalid_request",
    "message": "message"
  }
}
```

Common error codes are:

- `invalid_request`
- `not_found`
- `unauthorized`
- `forbidden`
- `internal_error`

## Authentication Errors

Missing credentials return `401` and set:

```http
WWW-Authenticate: Bearer realm="fbs"
```

Malformed, invalid, or unsupported credentials return `401`. Inactive users or callers without the required role return `403`.

## Resource Grants (mini-IAM)

Normalized resource grants are the sharing model for S3 data-plane access. They are **not** AWS IAM policies, bucket policies, or ACLs. See `plan/access-control/access-control.md`.

List grants on a bucket (admin or owner):

```http
GET /api/management/buckets/{bucket}/grants
```

Create grants (admin or owner). One row is created per action; duplicate active grants are idempotent:

```http
POST /api/management/buckets/{bucket}/grants
Content-Type: application/json

{
  "grantee_user_id": "user-uuid",
  "actions": ["s3:GetObject", "s3:ListBucket"],
  "key_prefix": "docs/",
  "note": "optional label"
}
```

Alternatively identify the grantee with `grantee_access_key_id` (resolved server-side to user id). Grantable actions:

- `s3:ListBucket`
- `s3:GetObject`
- `s3:PutObject`
- `s3:DeleteObject`
- `s3:ListMultipartUploadParts`
- `s3:AbortMultipartUpload`

`s3:CreateBucket` and `s3:DeleteBucket` are not grantable.

Update a grant (prefix, active, note):

```http
PATCH /api/management/buckets/{bucket}/grants/{grantID}
```

Delete a grant:

```http
DELETE /api/management/buckets/{bucket}/grants/{grantID}
```

List my grants:

```http
GET /api/management/grants/me
```

List grants for a user (admin only):

```http
GET /api/management/users/{userID}/grants
```

Transfer bucket ownership (admin or current owner):

```http
POST /api/management/buckets/{bucket}/transfer-ownership
Content-Type: application/json

{
  "new_owner_user_id": "user-uuid"
}
```

## Metrics

```http
GET /api/management/metrics
```

Returns:

```json
{
  "bucket_count": 1,
  "object_count": 12,
  "total_object_bytes": 4096,
  "user_count": 2,
  "active_user_count": 2
}
```

## Config Info

```http
GET /api/management/config
```

Returns runtime-safe configuration:

```json
{
  "region": "us-east-1",
  "dev_mode": false,
  "public_base_url": "https://storage.example.com",
  "limits": {
    "s3_max_keys": 1000,
    "s3_delete_objects": 1000,
    "management_object_list_limit": 1000,
    "management_activity_limit": 500
  }
}
```

## Buckets

List buckets:

```http
GET /api/management/buckets
```

Get one bucket:

```http
GET /api/management/buckets/{bucket}
```

Delete a bucket and all objects:

```http
DELETE /api/management/buckets/{bucket}
```

Empty a bucket but keep the bucket:

```http
POST /api/management/buckets/{bucket}/empty
```

Bucket summaries include:

- `name`
- `owner_id`
- `created_at`
- `object_count`
- `total_object_bytes`

Management bucket deletion is intentionally stronger than S3 `DeleteBucket`: it deletes all object metadata and backing files before deleting the bucket row.

## Objects

List objects:

```http
GET /api/management/buckets/{bucket}/objects?prefix=&delimiter=&cursor=&limit=100
```

Parameters:

- `prefix`: optional key prefix.
- `delimiter`: optional grouping delimiter.
- `cursor`: last key cursor from a previous response.
- `limit`: positive integer, default 100, capped at 1000.

Response:

```json
{
  "bucket": "photos",
  "prefix": "",
  "delimiter": "/",
  "limit": 100,
  "is_truncated": false,
  "next_cursor": "",
  "objects": [],
  "common_prefixes": ["2026/"]
}
```

Get object metadata:

```http
GET /api/management/buckets/{bucket}/objects/{key}
```

Object metadata includes:

- `key`
- `bucket`
- `size`
- `etag`
- `content_type`
- `created_at`
- `updated_at`

## Signed Public Object URLs

Create a signed public URL:

```http
POST /api/management/buckets/{bucket}/objects/{key}/public-url
Content-Type: application/json

{
  "expires_in_seconds": 3600,
  "response_content_disposition": "attachment; filename=\"image.jpg\""
}
```

This endpoint requires `FBS_PUBLIC_READ_SIGNING_SECRET` or `--public-read-signing-secret`. If signing is not configured, it returns `503`.

If `expires_in_seconds` is omitted, the configured default public read TTL is used. The requested TTL must be positive and no larger than the configured max TTL.

`response_content_disposition` is optional. If you omit it, FBS does not set `Content-Disposition`. Set it to a valid value such as `attachment; filename="image.jpg"` to force a download. The URL signature covers the value. Changing or adding the value later invalidates the URL.

Response:

```json
{
  "url": "https://storage.example.com/public/photos/a.jpg?expires=...&response-content-disposition=...&signature=...",
  "expires_at": "2026-05-17T12:00:00Z",
  "cache_control": "public, max-age=3600, must-revalidate"
}
```

Public reads are served from:

```http
GET /public/{bucket}/{key}?expires={unix_seconds}&signature={hex_hmac}[&response-content-disposition={value}]
HEAD /public/{bucket}/{key}?expires={unix_seconds}&signature={hex_hmac}[&response-content-disposition={value}]
```

Public read URLs do not use Bearer or SigV4 auth. They require exactly one `expires` and `signature` parameter, and may include one `response-content-disposition` parameter. The signature covers that optional value.
Public object GET and HEAD responses preserve the stored `Content-Type` and include `X-Content-Type-Options: nosniff`.

## Share Links

Share links are short, revocable URLs such as `https://storage.example.com/s/x7Kp2mQa9Z` that serve an object directly. They are an fbs extension, not part of the S3 API, and they do not need `FBS_PUBLIC_READ_SIGNING_SECRET`. These endpoints require the `admin` role.

Create a share link:

```http
POST /api/management/share-links
Content-Type: application/json

{
  "bucket": "videos",
  "key": "2026/clip.mp4",
  "alias": "clip-2026",
  "expires_in_seconds": 604800,
  "response_content_disposition": "inline; filename=\"clip.mp4\""
}
```

- `bucket` and `key` are required, and the object must exist.
- `alias` is optional. Without it, fbs generates a random 10-character base62 code. An alias must be 3–64 letters, digits, `-` or `_`, starting with a letter or digit. Aliases are easy to remember and therefore easy to guess, so use random codes for anything private. A taken alias returns `409`.
- `expires_in_seconds` is optional. Without it, the link does not expire until revoked.
- `response_content_disposition` is optional and is sent as `Content-Disposition` on every read.

Response (`201`):

```json
{
  "code": "clip-2026",
  "url": "https://storage.example.com/s/clip-2026",
  "bucket": "videos",
  "key": "2026/clip.mp4",
  "response_content_disposition": "inline; filename=\"clip.mp4\"",
  "created_by": "user-id",
  "expires_at": "2026-10-09T12:00:00Z",
  "created_at": "2026-10-02T12:00:00Z"
}
```

`expires_at` is `null` for links without an expiry.

List share links, optionally for one bucket:

```http
GET /api/management/share-links[?bucket={bucket}]
```

Revoke a share link:

```http
DELETE /api/management/share-links/{code}
```

Share links are served from:

```http
GET  /s/{code}[/{filename}]
HEAD /s/{code}[/{filename}]
```

The response is the object itself, not a redirect, so chat apps such as Discord unfurl it as direct media. It keeps the stored `Content-Type` and supports `Range` and conditional requests, which video players need for seeking. The optional trailing `{filename}` is ignored. It only lets a link end in a name like `clip.mp4`.

Reads send `Cache-Control: public, max-age=0, must-revalidate`, so caches revalidate and revocation takes effect quickly. Services that already copied the media, such as Discord's media proxy, may keep showing it.

A share link points at `bucket + key`, not at a fixed object version:

- Overwriting the key makes the link serve the new content.
- Deleting the object makes the link return `404`.
- Deleting the bucket deletes its share links.
- On every read, fbs checks that the link's creator is still an active user with read access to the object. Deactivating or deleting the creator disables their links.

Unknown, expired, revoked, and unauthorized links all return `404` with `Cache-Control: no-store`, so codes cannot be probed.

## Keys

List keys:

```http
GET /api/management/keys
```

Create a key:

```http
POST /api/management/keys
Content-Type: application/json

{
  "display_name": "CI uploader",
  "role": "member"
}
```

`role` is optional and defaults to `member`. Valid roles are `admin` and `member`.

Create response includes the raw Bearer token and SigV4 secret key once:

```json
{
  "key": {
    "id": "...",
    "display_name": "CI uploader",
    "access_key_id": "fbsa_...",
    "sigv4_access_key_id": "fbsv4_...",
    "role": "member",
    "is_active": true,
    "created_at": "...",
    "updated_at": "..."
  },
  "bearer_token": "fbsa_....secret",
  "sigv4": {
    "access_key_id": "fbsv4_...",
    "secret_key": "..."
  }
}
```

Patch a key:

```http
PATCH /api/management/keys/{id}
Content-Type: application/json

{
  "display_name": "CI uploader renamed",
  "is_active": true
}
```

At least one field is required. `display_name` must be non-empty. `is_active` must be boolean.

Delete a key:

```http
DELETE /api/management/keys/{id}
```

## Activity

List activity:

```http
GET /api/management/activity?bucket=&action=&limit=100
```

Parameters:

- `bucket`: optional bucket filter.
- `action`: optional action filter.
- `limit`: positive integer, default 100, capped at 500.

Activity is recorded for bucket creation/deletion, object writes/deletes/copies, multipart completion, bucket emptying, forced bucket deletion, and batch delete operations.

