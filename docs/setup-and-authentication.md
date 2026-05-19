# Setup And Authentication

## First-Start Bootstrap

Setup endpoints are available only from loopback clients. A request is loopback when the remote IP address is loopback.

Check setup status:

```bash
curl http://127.0.0.1:9000/api/setup/status
```

Response:

```json
{
  "bootstrap_required": true,
  "region": "us-east-1",
  "management_url": "http://127.0.0.1:9000/api/management",
  "s3_url": "http://127.0.0.1:9000"
}
```

Create the initial admin:

```bash
curl -X POST http://127.0.0.1:9000/api/setup/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"Admin User"}'
```

The response includes:

- `key`: user/key metadata safe to show later.
- `bearer_token`: raw Bearer token returned once.
- `sigv4.access_key_id`: SigV4 access key ID.
- `sigv4.secret_key`: raw SigV4 secret returned once.
- `region`: S3 region, currently `us-east-1`.
- `management_url` and `s3_url`.

Bootstrap is atomic. If any user already exists, `POST /api/setup/bootstrap` returns `409`.

## Bearer Tokens

Bearer auth uses:

```http
Authorization: Bearer {access_key_id}.{secret}
```

Bearer access key IDs are generated with the `fbsa_` prefix. Secret material is stored only as a SHA-256 hash in SQLite. Incoming tokens are split into access key ID and secret, the secret is hashed, and the hash is compared in constant time.

Bearer tokens are accepted by protected S3 routes and Management routes.

## fbs-web Login Token

Use a Bearer token to log in to `fbs-web`. On a fresh install, the initial admin token is returned once by the loopback-only bootstrap endpoint:

```bash
curl -X POST http://127.0.0.1:9000/api/setup/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"Admin User"}'
```

Use the `bearer_token` value from the response in `fbs-web`.

If the system is already bootstrapped and you have an admin Bearer token, create another admin token through the Management API:

```bash
curl -X POST http://127.0.0.1:9000/api/management/keys \
  -H 'Authorization: Bearer YOUR_ADMIN_TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"Web Admin","role":"admin"}'
```

The response includes a new `bearer_token`. Store it immediately. Bearer token secrets are not returned by later reads because only their hashes are stored.

## AWS SigV4

SigV4 credentials are generated with access key IDs using the `fbsv4_` prefix. The raw SigV4 secret key is stored because HMAC verification requires it. Ordinary user reads and management key listings do not return the secret key after creation.

Supported SigV4 forms:

- Header authentication with `Authorization: AWS4-HMAC-SHA256 ...`.
- Query-string presigned URL authentication with `X-Amz-Algorithm=AWS4-HMAC-SHA256`.

Rules:

- Service scope must be `s3`.
- Region is `us-east-1`.
- Signed headers must include `host`.
- Header-authenticated requests must be within 15 minutes of server time.
- Query-authenticated requests must have `X-Amz-Expires` between 1 and 604800 seconds.
- Query signatures older than 7 days are rejected even if malformed expiry data would otherwise allow them.
- `UNSIGNED-PAYLOAD` is accepted where implemented.

## Roles

Users have one of two roles:

- `admin`: can use S3 routes and Management API routes.
- `member`: can use S3 routes. Management API access is denied.

S3 bucket listing is role-aware. Admin users list all buckets. Member users list only buckets owned by their user ID.

## Inactive Users

Inactive users cannot authenticate. Management key patching can toggle `is_active`.

## Dev Mode

`--dev` or `FBS_DEV=true` enables a built-in admin principal:

- User ID: `dev-user`
- Display name: `Development User`
- Access key ID: `dev`
- Role: `admin`

Dev mode bypasses normal auth but is restricted to localhost-only bind addresses. The server refuses to start in dev mode when the listener is not `127.0.0.1`, `localhost`, or `::1`.

When dev mode creates buckets, the server ensures a matching `dev-user` row exists so bucket ownership still has a metadata owner.
