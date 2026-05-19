# Configuration

Configuration is loaded from environment variables and then overridden by command-line flags. Durations use Go duration strings such as `30s`, `15m`, or `24h`. Byte-size settings accept plain byte counts and parser-supported size strings.

## Defaults

| Setting | Default |
| --- | --- |
| HTTP address | `127.0.0.1:9000` |
| SQLite database | `./fbs.db` |
| Data directory | `./data` |
| Metadata cache | `536870912` bytes |
| S3 read `Cache-Control` | `private, max-age=0, must-revalidate` |
| Public read default TTL | `1h` |
| Public read maximum TTL | `24h` |
| HTTP read timeout | `15s` |
| HTTP write timeout | `30s` |
| HTTP idle timeout | `60s` |
| Shutdown timeout | `10s` |
| Multipart TTL | `24h` |
| Multipart cleanup interval | `1h` |
| CORS allowed origins | `http://localhost:3000`, `http://127.0.0.1:3000`, `http://localhost:5173`, `http://127.0.0.1:5173` |

## Environment Variables And Flags

| Environment variable | CLI flag | Description |
| --- | --- | --- |
| `FBS_HTTP_ADDR` | `--http-addr` | HTTP listen address. |
| `FBS_DB_PATH` | `--db-path` | SQLite database path or SQLite `file:` URI. |
| `FBS_DATA_DIR` | `--data-dir` | Object data root directory. |
| `FBS_DEV` | `--dev` | Enables local-only auth bypass. |
| `FBS_PUBLIC_BASE_URL` | `--public-base-url` | External base URL used in setup responses and signed public URLs. |
| `FBS_METADATA_CACHE_SIZE` | `--metadata-cache-size` | Metadata cache byte budget. Set `0` to disable repository cache wrappers. |
| `FBS_S3_CACHE_CONTROL` | `--s3-cache-control` | `Cache-Control` header for authenticated S3 reads. |
| `FBS_PUBLIC_READ_SIGNING_SECRET` | `--public-read-signing-secret` | Enables signed public reads. Must be at least 32 bytes. |
| `FBS_PUBLIC_READ_DEFAULT_TTL` | `--public-read-default-ttl` | Default TTL for generated public object URLs. |
| `FBS_PUBLIC_READ_MAX_TTL` | `--public-read-max-ttl` | Maximum TTL allowed when generating public object URLs. |
| `FBS_CORS_ALLOWED_ORIGINS` | `--cors-allowed-origins` | Comma-separated allowed browser origins. |
| `FBS_READ_TIMEOUT` | `--read-timeout` | HTTP server read timeout. |
| `FBS_WRITE_TIMEOUT` | `--write-timeout` | HTTP server write timeout. |
| `FBS_IDLE_TIMEOUT` | `--idle-timeout` | HTTP server idle timeout. |
| `FBS_SHUTDOWN_TIMEOUT` | `--shutdown-timeout` | Graceful shutdown timeout. |
| `FBS_MULTIPART_TTL` | `--multipart-ttl` | Age after which active multipart uploads become stale. |
| `FBS_MULTIPART_CLEANUP_INTERVAL` | `--multipart-cleanup-interval` | Background cleanup interval. Minimum `1m`. |

## Validation Rules

The server refuses to start when:

- HTTP address, database path, or data directory is empty.
- Metadata cache size is negative.
- `public-base-url` is not a valid request URI.
- Public read signing secret is set but shorter than 32 bytes.
- Public read TTLs are non-positive or default TTL exceeds max TTL.
- CORS origin list is empty.
- HTTP or shutdown timeouts are non-positive.
- Multipart TTL is non-positive.
- Multipart cleanup interval is below `1m`.
- Dev mode is enabled while binding to anything other than `127.0.0.1`, `localhost`, or `::1`.

## CORS

CORS is configured globally with:

- Allowed methods: `GET`, `HEAD`, `POST`, `PATCH`, `PUT`, `DELETE`, `OPTIONS`.
- Allowed headers include `Authorization`, content headers, and common S3 SigV4 headers.
- Exposed headers: `ETag` and `x-amz-bucket-region`.
- Credentials are allowed.
- Preflight max age is 300 seconds.

