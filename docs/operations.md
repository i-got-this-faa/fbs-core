# Operations

## Running Locally

Default run:

```bash
go run ./cmd/server
```

Local dev mode:

```bash
go run ./cmd/server --dev
```

Dev mode bypasses auth and is allowed only on localhost bind addresses.

Custom paths:

```bash
go run ./cmd/server \
  --http-addr 127.0.0.1:9000 \
  --db-path /var/lib/fbs/fbs.db \
  --data-dir /var/lib/fbs/data
```

## Health Checks

```http
GET /healthz
GET /readyz
```

Both return JSON:

```json
{"status":"ok"}
```

or:

```json
{"status":"ready"}
```

## Initial Setup

Bootstrap must be performed from the server host:

```bash
curl -X POST http://127.0.0.1:9000/api/setup/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"Admin User"}'
```

Store the returned Bearer token and SigV4 secret. They are not returned again by management reads.

## Public Base URL

Set `FBS_PUBLIC_BASE_URL` or `--public-base-url` when the service is behind a reverse proxy. This value is used in setup responses and generated public object URLs.

Example:

```bash
FBS_PUBLIC_BASE_URL=https://storage.example.com go run ./cmd/server
```

## Public Reads

Signed public reads are disabled unless a signing secret is configured:

```bash
FBS_PUBLIC_READ_SIGNING_SECRET='at-least-32-bytes-of-secret-material' \
go run ./cmd/server
```

Generate public URLs through the Management API:

```http
POST /api/management/buckets/{bucket}/objects/{key}/public-url
```

The public read path is reserved, so `public` is not a valid bucket name.

## Reverse Proxy Notes

For S3 compatibility, preserve:

- The HTTP method.
- The exact path and query string.
- The `Host` header used by SigV4 clients.
- `Authorization`, `X-Amz-Date`, `X-Amz-Content-SHA256`, and other signed headers.

If generated URLs should use an external hostname, configure `public-base-url` instead of relying on inbound request host detection.

## Backup

Back up SQLite and object data together. SQLite is the source of truth, but object rows reference files under the data directory. A consistent backup should include:

- SQLite database file and WAL-related files, or a SQLite online backup.
- The full data directory.

Avoid backing up only object files without SQLite metadata, because object keys, content type, ETags, sizes, ownership, and activity live in the database.

## Startup Cleanup

Every start reconciles the filesystem against metadata. It can delete:

- Non-multipart temporary files under `data/.tmp`.
- Object backing files without matching metadata rows.
- Multipart temp directories without matching upload rows.

This is expected. Do not put unrelated files under the configured data directory.

## Troubleshooting

### Bootstrap returns 403

The setup endpoints are loopback-only. Run the request from the server host using `127.0.0.1`, `localhost`, or another loopback address.

### Dev mode refuses to start

Dev mode requires a localhost-only bind address. Use `--http-addr 127.0.0.1:9000`, `localhost:9000`, or `[::1]:9000`.

### Management API returns 403

The credentials are valid but the user is inactive or does not have `admin` role. Use an admin key to list or patch users.

### S3 SigV4 requests fail

Check:

- Service is `s3`.
- Region is `us-east-1`.
- Signed headers include `host`.
- Proxy preserves signed headers and host.
- Server clock is close to client clock.

### Public URL returns 403

Check:

- Signing secret is configured.
- URL has exactly `expires` and `signature` query parameters.
- URL path has not been rewritten.
- The expiration timestamp is still in the future.

