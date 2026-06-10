# Support

Thank you for using **fbs-core** — a self-hosted, S3-compatible object storage server. This document explains where to find help, how to report bugs, and what to include when you do.

## Documentation

Before opening an issue, please check the existing documentation in [`docs/`](./docs/README.md):

| Document | What it covers |
|---|---|
| [Quickstart](./docs/quickstart.md) | Docker setup, first admin bootstrap, first S3 operations |
| [Configuration](./docs/configuration.md) | CLI flags, environment variables, defaults, and validation |
| [Setup & Authentication](./docs/setup-and-authentication.md) | Bootstrap endpoint, Bearer tokens, SigV4 credentials, roles, dev mode |
| [S3 API](./docs/s3-api.md) | Supported S3-compatible endpoints and known unsupported operations |
| [Management API](./docs/management-api.md) | Admin JSON endpoints for metrics, buckets, objects, keys, and public URLs |
| [Storage & Metadata](./docs/storage-and-metadata.md) | SQLite schema, disk layout, reconciliation, cache, and multipart internals |
| [Operations](./docs/operations.md) | Startup, backup, cleanup, CORS, public reads, deployment, and troubleshooting |
| [Development](./docs/development.md) | Repository layout, test strategy, and implementation conventions |

The [Operations troubleshooting section](./docs/operations.md) is a good first stop for common runtime problems.

## Getting Help

If the documentation doesn't answer your question, [open an issue](https://github.com/i-got-this-faa/fbs-core/issues/new) and use the **question** label. Please search [existing issues](https://github.com/i-got-this-faa/fbs-core/issues) first — your question may already have an answer.

## Reporting a Bug

Open an issue and include as much of the following as is relevant:

- **fbs-core version** — the release tag (e.g. `v0.1.0`) or commit hash
- **How you are running it** — Docker, `go run`, compiled binary, etc.
- **Operating system and architecture**
- **What you did** — the request, CLI command, or sequence of steps
- **What you expected to happen**
- **What actually happened** — error messages, HTTP response bodies, or unexpected behavior
- **Relevant server logs** — run with verbose logging enabled if possible

If you can reproduce the problem with a minimal example (e.g. a `curl` command or a short script), please include it.

> **Note:** The `public` bucket name is reserved for signed read URLs and cannot be used as an ordinary bucket name. If you are seeing unexpected `403` or routing errors, check this first.

## Reporting a Security Vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities. Instead, report them privately via [GitHub's private vulnerability reporting](https://github.com/i-got-this-faa/fbs-core/security/advisories/new) so they can be triaged and patched before disclosure.

## Feature Requests

Feature requests are welcome. Open an issue with the **enhancement** label and describe the use case. Because fbs-core is designed for single-node self-hosted deployments, proposals that fit that scope are most likely to be considered.

## Out of Scope

fbs-core is a backend storage engine; it does not provide a web dashboard or terminal dashboard directly — those are separate components that consume the Management API. Issues related to third-party S3 client compatibility should include the client name, version, and the specific operation that fails.
