# Quickstart

This quickstart runs `fbs-core` with Docker, creates the first admin credentials, verifies the Management API, and performs a basic S3 upload/download flow.

## 1. Start The Container

Use the published image from GitHub Container Registry:

```bash
docker volume create fbs-data

docker run -d \
  --name fbs-core \
  -p 9000:9000 \
  -v fbs-data:/var/lib/fbs \
  -e FBS_PUBLIC_BASE_URL=http://127.0.0.1:9000 \
  ghcr.io/i-got-this-faa/fbs-core:latest
```

The image stores SQLite and object data under `/var/lib/fbs`, listens on container port `9000`, and sets these defaults:

```text
FBS_HTTP_ADDR=0.0.0.0:9000
FBS_DB_PATH=/var/lib/fbs/fbs.db
FBS_DATA_DIR=/var/lib/fbs/data
```

If you are working from a local checkout instead of the published image:

```bash
docker build -t fbs-core:local .

docker run -d \
  --name fbs-core \
  -p 9000:9000 \
  -v fbs-data:/var/lib/fbs \
  -e FBS_PUBLIC_BASE_URL=http://127.0.0.1:9000 \
  fbs-core:local
```

Check health:

```bash
curl http://127.0.0.1:9000/healthz
curl http://127.0.0.1:9000/readyz
```

## 2. Bootstrap The First Admin

Setup endpoints are loopback-only. With Docker port mapping, a host request usually reaches the container from a bridge IP, not from loopback. Run bootstrap from inside the container:

```bash
docker exec fbs-core wget -qO- \
  --header='Content-Type: application/json' \
  --post-data='{"display_name":"Admin User"}' \
  http://127.0.0.1:9000/api/setup/bootstrap
```

Save these fields from the response:

- `bearer_token`
- `sigv4.access_key_id`
- `sigv4.secret_key`

They are returned once. Later Management API key listings do not expose raw secrets.

For `fbs-web`, the admin key value you paste into the connection screen is the raw `bearer_token` from this bootstrap response.

If bootstrap has already been completed, the command returns `409 conflict`.

## 3. Verify The Management API

Set your Bearer token:

```bash
export FBS_TOKEN='paste-bearer-token-here'
```

Call the Management API:

```bash
curl http://127.0.0.1:9000/api/management/metrics \
  -H "Authorization: Bearer ${FBS_TOKEN}"
```

List keys:

```bash
curl http://127.0.0.1:9000/api/management/keys \
  -H "Authorization: Bearer ${FBS_TOKEN}"
```

## 4. Connect fbs-web

If you are using `fbs-web`, start it separately and point it at this backend.

From the sibling `fbs-web` checkout:

```bash
cd ../fbs-web
bun install
bun run dev -- --host 127.0.0.1
```

Open the web app, then use:

- Backend URL: `http://127.0.0.1:9000`
- Admin key / token: the raw admin `bearer_token` returned by bootstrap

Use the bootstrap admin Bearer token, not the SigV4 access key ID or SigV4 secret key, for the fbs-web connection screen. fbs-web sends that value as `Authorization: Bearer ...` when calling the Management API.

The Docker examples above work with local fbs-web development because the default CORS origins include `http://localhost:5173`, `http://127.0.0.1:5173`, `http://localhost:3000`, and `http://127.0.0.1:3000`. If fbs-web is hosted on another origin, add it to `FBS_CORS_ALLOWED_ORIGINS` when starting `fbs-core`.

Example:

```bash
docker run -d \
  --name fbs-core \
  -p 9000:9000 \
  -v fbs-data:/var/lib/fbs \
  -e FBS_PUBLIC_BASE_URL=https://storage.example.com \
  -e FBS_CORS_ALLOWED_ORIGINS=https://dashboard.example.com \
  ghcr.io/i-got-this-faa/fbs-core:latest
```

## 5. Use The S3 API With AWS CLI

Install or configure AWS CLI, then export the SigV4 credentials from bootstrap:

```bash
export AWS_ACCESS_KEY_ID='paste-sigv4-access-key-id-here'
export AWS_SECRET_ACCESS_KEY='paste-sigv4-secret-key-here'
export AWS_DEFAULT_REGION='us-east-1'

aws configure set default.s3.addressing_style path
```

Create a bucket:

```bash
aws --endpoint-url http://127.0.0.1:9000 \
  s3api create-bucket \
  --bucket quickstart
```

Upload and download an object:

```bash
printf 'hello from fbs-core\n' > /tmp/fbs-hello.txt

aws --endpoint-url http://127.0.0.1:9000 \
  s3 cp /tmp/fbs-hello.txt s3://quickstart/hello.txt

aws --endpoint-url http://127.0.0.1:9000 \
  s3 cp s3://quickstart/hello.txt -
```

List objects:

```bash
aws --endpoint-url http://127.0.0.1:9000 \
  s3api list-objects-v2 \
  --bucket quickstart
```

## 6. Use The S3 API With Bearer Auth

Bearer auth is useful for simple HTTP clients:

```bash
curl -X PUT http://127.0.0.1:9000/bearer-bucket \
  -H "Authorization: Bearer ${FBS_TOKEN}"

curl -X PUT http://127.0.0.1:9000/bearer-bucket/example.txt \
  -H "Authorization: Bearer ${FBS_TOKEN}" \
  -H "Content-Type: text/plain" \
  --data 'uploaded with bearer auth'

curl http://127.0.0.1:9000/bearer-bucket/example.txt \
  -H "Authorization: Bearer ${FBS_TOKEN}"
```

## 7. Optional: Enable Signed Public Reads

Start the container with a signing secret of at least 32 bytes:

```bash
docker rm -f fbs-core

docker run -d \
  --name fbs-core \
  -p 9000:9000 \
  -v fbs-data:/var/lib/fbs \
  -e FBS_PUBLIC_BASE_URL=http://127.0.0.1:9000 \
  -e FBS_PUBLIC_READ_SIGNING_SECRET='replace-with-at-least-32-bytes-secret' \
  ghcr.io/i-got-this-faa/fbs-core:latest
```

Generate a public URL for an existing object:

```bash
curl -X POST http://127.0.0.1:9000/api/management/buckets/quickstart/objects/hello.txt/public-url \
  -H "Authorization: Bearer ${FBS_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"expires_in_seconds":3600}'
```

Open the returned `url` without auth before it expires.

## Docker Compose Example

```yaml
services:
  fbs-core:
    image: ghcr.io/i-got-this-faa/fbs-core:latest
    container_name: fbs-core
    ports:
      - "9000:9000"
    environment:
      FBS_PUBLIC_BASE_URL: http://127.0.0.1:9000
      FBS_CORS_ALLOWED_ORIGINS: http://localhost:3000,http://127.0.0.1:3000,http://localhost:5173,http://127.0.0.1:5173
      # FBS_PUBLIC_READ_SIGNING_SECRET: replace-with-at-least-32-bytes-secret
    volumes:
      - fbs-data:/var/lib/fbs
    restart: unless-stopped

volumes:
  fbs-data:
```

Start it:

```bash
docker compose up -d
```

Then run the same bootstrap command:

```bash
docker exec fbs-core wget -qO- \
  --header='Content-Type: application/json' \
  --post-data='{"display_name":"Admin User"}' \
  http://127.0.0.1:9000/api/setup/bootstrap
```

## Cleanup

Remove the container but keep data:

```bash
docker rm -f fbs-core
```

Remove the persistent data volume:

```bash
docker volume rm fbs-data
```
