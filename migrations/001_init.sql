-- Initialize the database schema (v1 -- matches runtime migration version 1).
-- SigV4 columns are added by migration 2 at application startup.

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

CREATE TABLE IF NOT EXISTS buckets (
    name       TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL REFERENCES users(id),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

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
    is_multipart   INTEGER NOT NULL DEFAULT 0,
    checksum_crc32    TEXT,
    checksum_crc32c   TEXT,
    checksum_crc64nvme TEXT,
    checksum_sha1     TEXT,
    checksum_sha256   TEXT,
    UNIQUE(bucket_name, key)
);

CREATE INDEX IF NOT EXISTS idx_objects_bucket_prefix ON objects(bucket_name, key);

CREATE TABLE IF NOT EXISTS multipart_uploads (
    id           TEXT PRIMARY KEY,
    bucket_name  TEXT NOT NULL REFERENCES buckets(name),
    key          TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completing', 'aborted')),
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    status_updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS multipart_parts (
    upload_id    TEXT NOT NULL REFERENCES multipart_uploads(id) ON DELETE CASCADE,
    part_number  INTEGER NOT NULL,
    size         INTEGER NOT NULL,
    etag         TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    checksum_crc32    TEXT,
    checksum_crc32c   TEXT,
    checksum_crc64nvme TEXT,
    checksum_sha1     TEXT,
    checksum_sha256   TEXT,
    PRIMARY KEY (upload_id, part_number)
);

CREATE TRIGGER IF NOT EXISTS validate_multipart_upload_status_insert
BEFORE INSERT ON multipart_uploads
FOR EACH ROW
WHEN NEW.status NOT IN ('active', 'completing', 'aborted')
BEGIN
    SELECT RAISE(ABORT, 'invalid multipart upload status');
END;

CREATE TRIGGER IF NOT EXISTS validate_multipart_upload_status_update
BEFORE UPDATE OF status ON multipart_uploads
FOR EACH ROW
WHEN NEW.status NOT IN ('active', 'completing', 'aborted')
BEGIN
    SELECT RAISE(ABORT, 'invalid multipart upload status');
END;
