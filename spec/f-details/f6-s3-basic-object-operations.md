# F6: S3 API - Basic Object Operations (Backend)

**Status:** TODO

## Summary

Implement the first functional S3-compatible object endpoints: `PUT`, `GET`, `HEAD`, and `DELETE`. This feature wires the HTTP layer from F1 to the metadata layer from F2 and the disk engine from F3, while establishing S3-style XML error responses and upload checksum validation.

## Scope

- Implement HTTP handlers for `PUT`, `GET`, `HEAD`, and `DELETE`
- Parse bucket name and object key from S3-style request paths
- Use F3 to write, open, read, and delete object data on disk
- Use F2 to upsert, read, list, and delete object metadata
- Return S3-compatible headers such as `ETag`, `Content-Length`, and `Last-Modified`
- Validate `Content-MD5` and `x-amz-checksum-*` headers on upload
- Return S3-style XML errors for object routes
- Add original black-box tests that verify protocol behavior, not implementation internals

## Prerequisites

- **F1 (Core HTTP Server & Routing):** DONE - provides the router and middleware registration point
- **F2 (SQLite Metadata Layer):** TODO - provides bucket and object repositories
- **F3 (Disk Storage Engine):** TODO - provides atomic writes, reads, deletes, and path sanitization

## Implementation Details

### Route Shape

Object routes should be mounted at the root path using bucket-first addressing:

| Method | Path | Purpose | Success Status |
|---|---|---|---|
| `PUT` | `/{bucket}/*` | Upload or overwrite an object | `200 OK` |
| `GET` | `/{bucket}/*` | Download an object | `200 OK` |
| `HEAD` | `/{bucket}/*` | Retrieve object metadata only | `200 OK` |
| `DELETE` | `/{bucket}/*` | Delete an object | `204 No Content` |

Notes:

- `chi.URLParam(r, "bucket")` extracts the bucket name
- `chi.URLParam(r, "*")` extracts the object key, including embedded `/`
- Requests without a key belong to bucket-level features (F7) and should not be handled here
- Multipart query parameters such as `uploadId` and `partNumber` belong to F8 and should not be handled here

Suggested registration:

```go
func RegisterObjectRoutes(r chi.Router, h *ObjectHandlers) {
    r.Put("/{bucket}/*", h.PutObject)
    r.Get("/{bucket}/*", h.GetObject)
    r.Head("/{bucket}/*", h.HeadObject)
    r.Delete("/{bucket}/*", h.DeleteObject)
}
```

### Handler Dependencies

Define a small handler bundle instead of passing repositories around ad hoc:

```go
type ObjectHandlers struct {
    Buckets metadata.BucketRepository
    Objects metadata.ObjectRepository
    Storage storage.DiskEngine
    Now     func() time.Time
    NewID   func() string
    Logger  *slog.Logger
}
```

Why these dependencies:

- `Buckets` verifies the bucket exists before object work begins
- `Objects` is the source of truth for lookup and deletion semantics
- `Storage` performs all disk I/O using F3's atomic write rules
- `Now` and `NewID` simplify deterministic tests

### Request Parsing and Validation

For every object request:

1. Extract `bucket` from the route parameter
2. Extract `key` from the wildcard route parameter
3. Reject empty keys with an S3 `InvalidRequest` or `NoSuchKey`-style error depending on route semantics
4. Confirm the bucket exists via `BucketRepository.GetByName`
5. For uploads, let F3 validate and sanitize the key before disk I/O

Bucket existence should be checked before attempting an upload so data is not written into a non-existent namespace.

### Upload Headers

Support these checksum-related request headers on `PUT`:

| Header | Meaning | Expected Encoding |
|---|---|---|
| `Content-MD5` | MD5 of the payload | base64 |
| `x-amz-checksum-sha256` | SHA-256 checksum | base64 |
| `x-amz-checksum-sha1` | SHA-1 checksum | base64 |
| `x-amz-checksum-crc32` | CRC32 checksum | base64 |
| `x-amz-checksum-crc32c` | CRC32C checksum | base64 |

Minimum required by the spec is `Content-MD5` plus the `x-amz-checksum-*` family. If full checksum-family coverage is staged, `Content-MD5`, `sha1`, and `sha256` should land first because they are simplest to implement in pure Go.

### PUT Flow

`PUT` must preserve the write-before-metadata rule from `SPEC.md`:

1. Verify the bucket exists
2. Read request metadata: `Content-Type`, checksum headers, object key
3. Construct checksum hashers based on present headers
4. Stream the request body through a checksum pipeline into `storage.Write`
5. Receive `storagePath` and `size` from F3
6. Compare computed digests against all provided checksum headers
7. If any checksum mismatches, best-effort delete the just-written file and return a checksum error
8. Create or upsert the `objects` row in SQLite with:
   - new object ID
   - bucket name
   - key
   - size
   - computed MD5 hex as `ETag`
   - content type (default `application/octet-stream`)
   - storage path returned by F3
   - timestamps
9. Return `200 OK` with the quoted `ETag` header

Recommended checksum pipeline:

```go
md5h := md5.New()
writers := []io.Writer{md5h}

if sha256Header != "" {
    writers = append(writers, sha256.New())
}

tee := io.TeeReader(r.Body, io.MultiWriter(writers...))
storagePath, size, err := disk.Write(ctx, bucket, key, tee)
```

Implementation note:

- F3 currently finalizes the file before returning
- That is acceptable as long as checksum mismatch prevents metadata commit
- On mismatch, F6 should call `Storage.Delete(ctx, storagePath)` best-effort so the bad file does not survive until reconciliation

### ETag Rules

For single-part uploads in F6:

- Compute the MD5 of the uploaded payload
- Store the lowercase hex digest in `objects.etag`
- Return the header as a quoted string: `ETag: "<hex>"`

This matches the common S3 behavior for non-multipart uploads and gives F8 a clear distinction later when multipart ETags are introduced.

### GET Flow

`GET` should:

1. Verify the bucket exists
2. Load object metadata by `(bucket, key)` from SQLite
3. If no row exists, return `NoSuchKey`
4. Open the on-disk file via `Storage.Open` or `Storage.Read`
5. Set response headers from metadata:
   - `ETag`
   - `Content-Length`
   - `Last-Modified`
   - `Content-Type`
6. Stream the body to the client

For F6, plain `io.Copy` is enough. F9 can later replace the streaming path with `http.ServeContent` for sendfile/range handling.

If metadata exists but the file is missing on disk, treat it as an internal consistency failure:

- log the bucket, key, and storage path
- return `500 InternalError`

Do not translate that condition to `NoSuchKey`, because SQLite is the declared source of truth.

### HEAD Flow

`HEAD` shares the lookup path with `GET` but must not write a body.

Behavior:

- Same object existence checks as `GET`
- Same response headers as `GET`
- Same error mapping as `GET`
- Zero response body bytes

Implementation can factor out a `loadObjectForRead(...)` helper used by both handlers.

### DELETE Flow

Delete ordering matters for correctness.

Recommended sequence:

1. Verify the bucket exists
2. Load metadata row by `(bucket, key)`
3. If no row exists, return `204 No Content` to preserve S3-style idempotent delete behavior
4. Delete the metadata row first
5. Best-effort delete the file from disk using the stored `storage_path`
6. Return `204 No Content`

Why metadata first:

- SQLite is the source of truth
- If file deletion succeeds but metadata deletion fails, the API would expose a row pointing to a missing file
- If metadata deletion succeeds but file deletion fails, the leftover file becomes an orphan that reconciliation can safely clean later

If the storage deletion fails after metadata deletion:

- log the failure
- still prefer returning `204 No Content` unless the team decides storage cleanup failures must be surfaced immediately

That choice favors correct object visibility semantics over synchronous leak reporting.

### Metadata Contract

F6 is responsible for populating these object fields on create or overwrite:

```go
obj := &metadata.Object{
    ID:          newID(),
    BucketName:  bucket,
    Key:         key,
    Size:        size,
    ETag:        md5Hex,
    ContentType: contentType,
    StoragePath: storagePath,
    CreatedAt:   now,
    UpdatedAt:   now,
}
```

Overwrite behavior:

- Same logical `(bucket, key)` should upsert the existing object row
- `updated_at` should reflect the latest successful upload
- Old data is replaced because F3 resolves a deterministic final path from bucket and key

### S3 Error Formatting

Object routes should emit XML errors instead of JSON.

Base structure:

```xml
<Error>
  <Code>NoSuchKey</Code>
  <Message>The specified key does not exist.</Message>
  <Resource>/bucket/path/to/object.txt</Resource>
  <RequestId>local-0001</RequestId>
</Error>
```

Suggested helper:

```go
type S3Error struct {
    Code       string `xml:"Code"`
    Message    string `xml:"Message"`
    Resource   string `xml:"Resource,omitempty"`
    RequestID  string `xml:"RequestId,omitempty"`
}

func WriteS3Error(w http.ResponseWriter, r *http.Request, status int, code, message string)
```

Response requirements:

- `Content-Type: application/xml`
- Correct HTTP status code
- `HEAD` errors should avoid emitting a response body

### Error Mapping

| Condition | HTTP Status | S3 Code |
|---|---|---|
| Bucket does not exist | `404 Not Found` | `NoSuchBucket` |
| Object row not found | `404 Not Found` | `NoSuchKey` |
| Empty or invalid key | `400 Bad Request` | `InvalidRequest` |
| Malformed `Content-MD5` | `400 Bad Request` | `InvalidDigest` |
| Malformed `x-amz-checksum-*` header | `400 Bad Request` | `InvalidRequest` |
| Checksum mismatch | `400 Bad Request` | `BadDigest` |
| Authenticated user lacks permission | `403 Forbidden` | `AccessDenied` |
| Metadata exists but file missing | `500 Internal Server Error` | `InternalError` |
| Unexpected repository or storage failure | `500 Internal Server Error` | `InternalError` |

### Auth Integration

F6 should sit behind the auth framework from F4, but the handlers themselves should only rely on request context, not parse credentials.

Handler expectations:

- a valid `Principal` is already present in `r.Context()` for protected routes
- future authorization checks can inspect bucket ownership or role if needed
- dev mode works automatically because F4 injects a synthetic principal

This keeps object handlers focused on S3 semantics instead of authentication mechanics.

### Consistency Rules

The key data-integrity rules for F6 are:

- `PUT`: disk first, metadata second
- `DELETE`: metadata first, disk second
- `GET`/`HEAD`: metadata decides existence; missing backing files are internal failures

These rules align with the project requirement that SQLite is the single source of truth.

### Package Structure

```text
internal/s3/
  routes.go               # RegisterObjectRoutes and future bucket/multipart registration
  object_handlers.go      # PUT/GET/HEAD/DELETE handlers
  object_read.go          # Shared read-path helpers for GET and HEAD
  checksum.go             # Header parsing and checksum validation helpers
  errors.go               # S3 error definitions and mappings
  xml.go                  # XML response writers
  object_handlers_test.go # Black-box HTTP tests for object operations
```

### Test Plan

| Test | What It Verifies |
|---|---|
| `TestPutObject` | `PUT` stores data and metadata, returns `200` and quoted `ETag` |
| `TestPutObject_DefaultContentType` | Missing `Content-Type` falls back to `application/octet-stream` |
| `TestPutObject_ContentMD5` | Valid `Content-MD5` is accepted |
| `TestPutObject_BadDigest` | Checksum mismatch returns `400 BadDigest` and does not leave committed metadata |
| `TestPutObject_Overwrite` | Re-uploading the same key updates metadata and replaces the data |
| `TestPutObject_NestedKey` | Keys containing `/` are stored and retrievable |
| `TestPutObject_NoSuchBucket` | Upload to a missing bucket returns `404 NoSuchBucket` |
| `TestGetObject` | `GET` returns full body and expected headers |
| `TestGetObject_NoSuchKey` | Missing object returns `404 NoSuchKey` XML |
| `TestHeadObject` | `HEAD` returns headers without a response body |
| `TestGetObject_MissingBackingFile` | Metadata-present/file-missing condition returns `500 InternalError` |
| `TestDeleteObject` | Existing object delete returns `204` and removes metadata |
| `TestDeleteObject_Idempotent` | Deleting a missing object still returns `204` |
| `TestDeleteObject_NoSuchBucket` | Delete on a missing bucket returns `404 NoSuchBucket` |
| `TestS3XMLErrors` | XML error body matches the expected S3 structure |

Tests should exercise the real router with a temp database and temp data directory so protocol behavior is validated end to end.

## Dependencies

- Go stdlib: `net/http`, `encoding/xml`, `io`, `crypto/md5`, `crypto/sha1`, `crypto/sha256`, `hash/crc32`, `time`
- F2 repositories: `BucketRepository`, `ObjectRepository`
- F3 storage engine: `DiskEngine`
- F4 auth context helpers for the authenticated principal

## Interfaces Provided to Other Features

- **`RegisterObjectRoutes(...)`** - consumed by the main router setup to mount object endpoints
- **`ObjectHandlers`** - shared handler bundle reused by future S3 route registration
- **Checksum helpers** - reusable by F8 multipart upload validation
- **`WriteS3Error(...)`** - reusable by F7 and F8 so all S3 routes share one XML error format
- **Read-path helper extraction** - gives F9 a clean seam to replace `GET` body streaming with `http.ServeContent`
