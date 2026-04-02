# F2: SQLite Metadata Layer (Backend)

**Status:** TODO

## Summary

Design and implement the SQLite-backed metadata store that serves as the single source of truth for all object storage state. This layer manages users, buckets, objects, and multipart upload tracking with high-concurrency tuning.

## Scope

- Design the SQLite schema (tables: `users`, `buckets`, `objects`, `multipart_uploads`)
- Configure SQLite for high concurrency (WAL mode, `busy_timeout=5000`, `synchronous=NORMAL`)
- Create Go repository interfaces for CRUD operations on metadata
- Add `created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP` to `multipart_uploads` for stale upload tracking

## Prerequisites

- **F1 (Core HTTP Server & Routing):** DONE - provides `config.Config` struct to extend with DB path settings

## Implementation Details

### Configuration Extension

Extend `config.Config` with:

| Setting | Flag | Env Var | Default |
|---|---|---|---|
| SQLite database path | `--db-path` | `FBS_DB_PATH` | `./fbs.db` |

### SQLite Connection Setup

Open the database with the following pragmas applied at connection time:

```sql
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA synchronous = NORMAL;
PRAGMA foreign_keys = ON;
```

- **WAL mode** enables concurrent reads while a write is in progress
- **busy_timeout=5000** causes writers to retry for up to 5 seconds instead of immediately returning `SQLITE_BUSY`
- **synchronous=NORMAL** balances durability and performance (safe with WAL)
- **foreign_keys=ON** enforces referential integrity

Use `database/sql` with `github.com/mattn/go-sqlite3` (CGo) or `modernc.org/sqlite` (pure Go). Decision should consider deployment constraints (CGo cross-compilation vs pure Go portability).

### Schema

#### `users` table

```sql
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    display_name  TEXT NOT NULL,
    access_key_id TEXT NOT NULL UNIQUE,
    secret_hash   TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    is_active     INTEGER NOT NULL DEFAULT 1,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

- `id`: UUID v4 primary key
- `access_key_id`: unique identifier used for S3 SigV4 auth (F5) and bearer token lookup (F4)
- `secret_hash`: SHA-256 hash of the secret/token (never stored in plaintext per spec)
- `role`: authorization level (`admin` for management API, `member` for S3 ops)
- `is_active`: soft-disable without deletion

#### `buckets` table

```sql
CREATE TABLE IF NOT EXISTS buckets (
    name       TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL REFERENCES users(id),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

- `name`: S3 bucket name, must conform to S3 naming rules (3-63 chars, lowercase, no dots adjacent to hyphens, etc.)
- `owner_id`: references the creating user

#### `objects` table

```sql
CREATE TABLE IF NOT EXISTS objects (
    id             TEXT PRIMARY KEY,
    bucket_name    TEXT NOT NULL REFERENCES buckets(name),
    key            TEXT NOT NULL,
    size           INTEGER NOT NULL,
    etag           TEXT NOT NULL,
    content_type   TEXT NOT NULL DEFAULT 'application/octet-stream',
    storage_path   TEXT NOT NULL,
    created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(bucket_name, key)
);

CREATE INDEX IF NOT EXISTS idx_objects_bucket_prefix ON objects(bucket_name, key);
```

- `id`: UUID v4 primary key
- `bucket_name` + `key`: unique constraint enforcing one object per logical key
- `etag`: computed MD5 hex digest stored on upload (F6)
- `storage_path`: relative path on disk under the data root (F3)
- Composite index on `(bucket_name, key)` for efficient `ListObjectsV2` prefix queries (F7)

#### `multipart_uploads` table

```sql
CREATE TABLE IF NOT EXISTS multipart_uploads (
    id          TEXT PRIMARY KEY,
    bucket_name TEXT NOT NULL REFERENCES buckets(name),
    key         TEXT NOT NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS multipart_parts (
    upload_id   TEXT NOT NULL REFERENCES multipart_uploads(id) ON DELETE CASCADE,
    part_number INTEGER NOT NULL,
    size        INTEGER NOT NULL,
    etag        TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (upload_id, part_number)
);
```

- `multipart_uploads.id`: UUID v4, returned as the `UploadId` in S3 API responses
- `created_at`: used by F8's background goroutine to identify stale uploads (older than configurable TTL, default 24h)
- `multipart_parts`: tracks individual parts with `ON DELETE CASCADE` to simplify abort cleanup

### Repository Interfaces

Define Go interfaces in `internal/store/` (or `internal/metadata/`):

```go
// internal/store/store.go

type UserRepository interface {
    Create(ctx context.Context, user *User) error
    GetByID(ctx context.Context, id string) (*User, error)
    GetByAccessKeyID(ctx context.Context, accessKeyID string) (*User, error)
    List(ctx context.Context) ([]User, error)
    Update(ctx context.Context, user *User) error
    Delete(ctx context.Context, id string) error
}

type BucketRepository interface {
    Create(ctx context.Context, bucket *Bucket) error
    GetByName(ctx context.Context, name string) (*Bucket, error)
    List(ctx context.Context) ([]Bucket, error)
    Delete(ctx context.Context, name string) error
}

type ObjectRepository interface {
    Create(ctx context.Context, obj *Object) error
    GetByKey(ctx context.Context, bucketName, key string) (*Object, error)
    List(ctx context.Context, bucketName, prefix, startAfter string, maxKeys int) ([]Object, bool, error)
    Delete(ctx context.Context, bucketName, key string) error
    DeleteAllInBucket(ctx context.Context, bucketName string) error
}

type MultipartUploadRepository interface {
    Create(ctx context.Context, upload *MultipartUpload) error
    GetByID(ctx context.Context, id string) (*MultipartUpload, error)
    Delete(ctx context.Context, id string) error
    ListStale(ctx context.Context, olderThan time.Time) ([]MultipartUpload, error)
    AddPart(ctx context.Context, part *MultipartPart) error
    ListParts(ctx context.Context, uploadID string) ([]MultipartPart, error)
}
```

### Domain Models

```go
type User struct {
    ID           string
    DisplayName  string
    AccessKeyID  string
    SecretHash   string
    Role         string
    IsActive     bool
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type Bucket struct {
    Name      string
    OwnerID   string
    CreatedAt time.Time
}

type Object struct {
    ID          string
    BucketName  string
    Key         string
    Size        int64
    ETag        string
    ContentType string
    StoragePath string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type MultipartUpload struct {
    ID         string
    BucketName string
    Key        string
    CreatedAt  time.Time
}

type MultipartPart struct {
    UploadID    string
    PartNumber  int
    Size        int64
    ETag        string
    StoragePath string
    CreatedAt   time.Time
}
```

### Database Initialization

Provide a `store.Open(dbPath string) (*sql.DB, error)` function that:

1. Opens the SQLite database at the given path (creates if not exists)
2. Applies the pragma settings
3. Runs the schema migrations (create tables if not exist)
4. Returns the configured `*sql.DB`

### Package Structure

```
internal/store/
  store.go          # Open(), interface definitions, domain models
  user.go           # UserRepository SQLite implementation
  bucket.go         # BucketRepository SQLite implementation
  object.go         # ObjectRepository SQLite implementation
  multipart.go      # MultipartUploadRepository SQLite implementation
  migrations.go     # Schema DDL statements and migration runner
```

## Test Plan

| Test | What It Verifies |
|---|---|
| `TestOpenDB` | Database opens, pragmas are set correctly (query `PRAGMA journal_mode`, etc.) |
| `TestMigrations` | Schema tables are created, re-running migrations is idempotent |
| `TestUserCRUD` | Create, read, update, delete users; unique constraint on `access_key_id` |
| `TestBucketCRUD` | Create, list, delete buckets; foreign key constraint on `owner_id` |
| `TestObjectCRUD` | Create, get by key, list with prefix/pagination, delete; unique constraint on `(bucket_name, key)` |
| `TestObjectUpsert` | Re-uploading the same key replaces the previous row |
| `TestMultipartLifecycle` | Create upload, add parts, list parts ordered by part_number, delete cascades parts |
| `TestListStaleUploads` | Uploads older than TTL are returned by `ListStale` |

Tests should use an in-memory SQLite database (`:memory:`) or a temp file for isolation.

## Dependencies

- `database/sql` (stdlib)
- `modernc.org/sqlite` or `github.com/mattn/go-sqlite3` - SQLite driver
- `github.com/google/uuid` - UUID generation

## Interfaces Provided to Other Features

- **Repository interfaces** - consumed by F4 (user auth), F5 (SigV4 user lookup), F6/F7/F8 (object/bucket/multipart operations), F10 (management API)
- **`store.Open()`** - called from `main.go` during startup; DB handle passed to repository constructors
- **Domain models** - shared data types used across all backend features
