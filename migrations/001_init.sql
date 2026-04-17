-- Initialize the database schema
--users
CREATE TABLE IF NOT EXISTS users(
    id              TEXT PRIMARY KEY,
    display_name    TEXT NOT NULL,
    access_key_id   TEXT NOT NULL UNIQUE,
    secret_hash     TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'member' CHECK(role IN('admin','member')),
    is_active       INTEGER NOT NULL DEFAULT 1,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
);
-- buckets
CREATE TABLE IF NOT EXISTS buckets(
    name            TEXT PRIMARY KEY,
    owner_id        TEXT NOT NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (bucket_name) REFERENCES buckets(name) ON DELETE CASCADE
);
-- objects
CREATE TABLE IF NOT EXISTS objects(
    id              TEXT PRIMARY KEY,
    bucket_name     TEXT NOT NULL,
    key             TEXT NOT NULL,
    size            INTEGER NOT NULL,
    etag            TEXT NOT NULL,
    content_type    TEXT NOT NULL,
    storage_path    TEXT NOT NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (bucket_name) REFERENCES buckets(name) ON DELETE CASCADE 
); 
-- indexes
CREATE INDEX IF NOT EXISTS idx_objects_bucket_key ON objects(bucket_name, key);
-- multipart uploads
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
    FOREIGN KEY (bucket_name) REFERENCES buckets(name) ON DELETE CASCADE
);