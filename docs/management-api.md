# Management API

The Management API is a JSON admin API under `/api/management`. All routes require authentication and the `admin` role. Responses use `Cache-Control: no-store`.

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

Malformed, invalid, or unsupported credentials return `401`. Inactive users or non-admin users return `403`.

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
  "expires_in_seconds": 3600
}
```

This endpoint requires `FBS_PUBLIC_READ_SIGNING_SECRET` or `--public-read-signing-secret`. If signing is not configured, it returns `503`.

If `expires_in_seconds` is omitted, the configured default public read TTL is used. The requested TTL must be positive and no larger than the configured max TTL.

Response:

```json
{
  "url": "https://storage.example.com/public/photos/a.jpg?expires=...&signature=...",
  "expires_at": "2026-05-17T12:00:00Z",
  "cache_control": "public, max-age=3600, must-revalidate"
}
```

Public reads are served from:

```http
GET /public/{bucket}/{key}?expires={unix_seconds}&signature={hex_hmac}
HEAD /public/{bucket}/{key}?expires={unix_seconds}&signature={hex_hmac}
```

Public read URLs do not use Bearer or SigV4 auth. They require exactly the `expires` and `signature` query parameters.

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

