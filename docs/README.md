# fbs-core Documentation

`fbs-core` is a single-node, self-hosted object storage service with an S3-compatible HTTP API and an admin-focused JSON Management API. It stores object bytes on the local filesystem and keeps object, bucket, user, activity, and multipart state in SQLite.

This documentation describes the completed backend as implemented in this repository.

## Documents

- [Architecture](./architecture.md): runtime components, request routing, and data consistency model.
- [Quickstart](./quickstart.md): Docker image setup, first admin bootstrap, and first S3 operations.
- [Configuration](./configuration.md): command-line flags, environment variables, defaults, and validation.
- [Setup And Authentication](./setup-and-authentication.md): first-start bootstrap, Bearer tokens, SigV4 credentials, roles, and dev mode.
- [S3 API](./s3-api.md): supported S3-compatible endpoints, request behavior, and known unsupported operations.
- [Management API](./management-api.md): admin JSON endpoints for metrics, buckets, objects, keys, activity, and public URLs.
- [Storage And Metadata](./storage-and-metadata.md): SQLite schema, migrations, disk layout, reconciliation, cache behavior, and multipart internals.
- [Operations](./operations.md): startup, backup, cleanup, CORS, public reads, deployment, and troubleshooting.
- [Development](./development.md): repository layout, test strategy, and implementation conventions.

## Quick Start

For the full Docker setup flow, see [Quickstart](./quickstart.md).

Run the server from source with defaults:

```bash
go run ./cmd/server
```

The default listener is `127.0.0.1:9000`, the default SQLite database is `./fbs.db`, and the default data directory is `./data`.

On a new database, create the initial admin from the server host:

```bash
curl -X POST http://127.0.0.1:9000/api/setup/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"Admin User"}'
```

The bootstrap response returns the Bearer token and SigV4 secret key once. Store them immediately.

## API Families

`fbs-core` exposes four HTTP surfaces:

- Health: `GET /healthz` and `GET /readyz`.
- Setup: loopback-only `GET /api/setup/status` and `POST /api/setup/bootstrap`.
- Management: admin-only JSON endpoints under `/api/management`.
- S3-compatible API: bucket and object operations at root paths such as `PUT /{bucket}`, `GET /{bucket}/{key}`, and multipart endpoints.

Protected S3 and Management routes accept Bearer tokens and AWS Signature Version 4 credentials. Management routes additionally require the `admin` role.
