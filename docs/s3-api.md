# S3 API

The S3-compatible API is rooted at `/`. Protected S3 routes accept Bearer tokens or AWS SigV4 credentials.

The implemented region is `us-east-1`.

## Supported Bucket Operations

| Operation | Request | Notes |
| --- | --- | --- |
| List buckets | `GET /` | Admins see all buckets. Members see owned buckets. |
| Create bucket | `PUT /{bucket}` | Empty body or `CreateBucketConfiguration` with empty or `us-east-1` location. |
| Head bucket | `HEAD /{bucket}` | Returns existence/authorization-compatible S3 response. |
| Get bucket location | `GET /{bucket}?location` | Returns region information. |
| List objects v1 | `GET /{bucket}` or `GET /{bucket}?list-type=1` | Supports `prefix`, `delimiter`, `marker`, `max-keys`, `encoding-type=url`. |
| List objects v2 | `GET /{bucket}?list-type=2` | Supports `prefix`, `delimiter`, `start-after`, `continuation-token`, `max-keys`, `encoding-type=url`. |
| Delete bucket | `DELETE /{bucket}` | Requires bucket to be empty. |
| Delete objects | `DELETE /{bucket}?delete` | Deletes up to 1000 objects from an XML request body. |

## Supported Object Operations

| Operation | Request | Notes |
| --- | --- | --- |
| Put object | `PUT /{bucket}/{key}` | Stores object, computes MD5 ETag, validates checksums when present. |
| Get object | `GET /{bucket}/{key}` | Uses `http.ServeContent`; supports range and conditional behavior from the standard library. |
| Head object | `HEAD /{bucket}/{key}` | Same metadata headers as GET without body. |
| Delete object | `DELETE /{bucket}/{key}` | Idempotent success for missing objects. |
| Copy object | `PUT /{bucket}/{key}` with `x-amz-copy-source` | Supports copy and content-type replacement via `x-amz-metadata-directive`. |

Object reads return:

- `ETag`
- `Content-Length`
- `Last-Modified`
- `Content-Type`
- configured `Cache-Control`

## Multipart Uploads

| Operation | Request | Notes |
| --- | --- | --- |
| Create multipart upload | `POST /{bucket}/{key}?uploads` | Returns upload ID. |
| Upload part | `PUT /{bucket}/{key}?partNumber={n}&uploadId={id}` | Part number must be 1 through 10000. |
| Complete multipart upload | `POST /{bucket}/{key}?uploadId={id}` | Requires XML part list in ascending order. |
| Abort multipart upload | `DELETE /{bucket}/{key}?uploadId={id}` | Deletes metadata and uploaded part files. |

Completion validates:

- All requested part numbers exist.
- Requested ETags match stored part ETags.
- Parts are strictly ascending with no duplicates.
- Every part except the last is at least 5 MiB by default.

Multipart ETags use the standard MD5-of-part-MD5s format with a `-{part_count}` suffix.

## Checksums

Put object and upload part compute MD5 while streaming. The computed MD5 hex string is stored as the object or part ETag.

The server validates these request headers when present:

- `Content-MD5`
- `X-Amz-Content-SHA256`, except `UNSIGNED-PAYLOAD`
- `x-amz-checksum-sha1`
- `x-amz-checksum-sha256`
- `x-amz-checksum-crc32`
- `x-amz-checksum-crc32c`

Invalid digest encoding returns an S3 digest error. Checksum mismatch rejects the upload before metadata commit and deletes the new backing file.

Multi-object delete requires a signed digest:

- `Content-MD5`, or
- `X-Amz-Content-SHA256` with a real SHA-256 payload hash.

Unsigned delete digests are rejected.

## Bucket Name Rules

Bucket names must:

- Be 3 to 63 characters.
- Be lowercase.
- Use only lowercase letters, digits, `-`, and `.`.
- Start and end with a letter or digit.
- Not look like an IP address.
- Not contain `..`, `.-`, or `-.`.
- Not be `public`, which is reserved for signed public read URLs.

## Object Key Rules

Object keys must:

- Be non-empty.
- Be at most 1024 bytes.
- Be valid UTF-8.
- Not start with `/`.
- Not equal `.` or `..`.
- Not contain null bytes, newlines, carriage returns, or path traversal segments.

Keys may contain `/`. The final object backing filename is still an opaque UUID path; the key is stored in metadata.

## Listing

`max-keys` defaults to 1000 and is capped at 1000.

ListObjectsV2 continuation tokens are base64 URL encoding of the last returned cursor key. Tokens longer than 1500 characters, or decoded keys longer than 1024 bytes, are rejected.

When `delimiter` is present, keys are grouped into common prefixes at the first delimiter after the requested prefix.

`encoding-type=url` URL-encodes keys, prefixes, delimiters, markers, and start-after values in list responses.

## Unsupported S3 Features

Requests for these features currently return S3-style not-implemented behavior:

- ACL operations.
- Bucket CORS and policy operations through S3 routes.
- Bucket versioning.
- Object version IDs in copy source.
- Listing active multipart uploads through bucket-level `?uploads`.
- Operations not listed above.

