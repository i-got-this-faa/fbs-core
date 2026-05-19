# Storage And Metadata

## SQLite

SQLite is opened through the pure-Go `modernc.org/sqlite` driver. Runtime DSNs include:

- `_pragma=journal_mode(WAL)`
- `_pragma=busy_timeout(5000)`
- `_pragma=synchronous(NORMAL)`
- `_pragma=foreign_keys(ON)`

Migrations are stored in `migrations/migration.go` and recorded in `schema_migrations`.

## Tables

### `users`

Stores Bearer and SigV4 credential metadata.

Important columns:

- `id`
- `display_name`
- `access_key_id`
- `secret_hash`
- `sigv4_access_key_id`
- `sigv4_secret_key`
- `role`
- `is_active`
- `created_at`
- `updated_at`

Bearer secrets are stored as SHA-256 hashes. SigV4 secret keys are stored raw for HMAC verification but are cleared from ordinary repository reads.

### `buckets`

Stores bucket metadata:

- `name`
- `owner_id`
- `created_at`

### `objects`

Stores object metadata:

- `id`
- `bucket_name`
- `key`
- `size`
- `etag`
- `content_type`
- `storage_path`
- `created_at`
- `updated_at`

`UNIQUE(bucket_name, key)` allows object upserts. The `storage_path` points to an opaque backing file path under the data directory.

### `multipart_uploads`

Tracks multipart upload sessions:

- `id`
- `bucket_name`
- `key`
- `content_type`
- `status`
- `created_at`
- `status_updated_at`

Valid statuses are `active`, `completing`, and `aborted`.

### `multipart_parts`

Tracks uploaded parts:

- `upload_id`
- `part_number`
- `size`
- `etag`
- `storage_path`
- `created_at`

Primary key is `(upload_id, part_number)`.

### `object_activity`

Stores activity events:

- `id`
- `action`
- `bucket_name`
- `object_key`
- `size`
- `etag`
- `actor_user_id`
- `created_at`

## Disk Layout

The storage engine resolves the configured data directory to an absolute path and creates:

```text
data/
  .tmp/
    multipart/
      {upload-id}/
        {part-number}.tmp-{uuid}
    {uuid}.tmp
  {bucket}/
    {uuid}
```

Object keys are not final filenames. A key such as `photos/2026/a.jpg` can be stored as a row with `key = photos/2026/a.jpg` and `storage_path = photos/{uuid}`.

## Object Writes

Object writes are atomic from the perspective of metadata:

- The request body is copied to a temporary file.
- The temporary file is synced and renamed to a unique final path.
- Metadata is inserted or upserted.
- On overwrite, the old backing file is deleted only after the new metadata row is committed.

If the metadata commit fails, the new backing file is deleted.

## Deletes

Object deletion removes metadata first and then deletes the backing file. Missing objects are treated as successful no-op deletes on the S3 single-object delete path.

Bucket emptying and Management bucket deletion use the shared `objectops.EmptyBucket` helper to enumerate all objects, delete metadata, and remove backing files.

## Reconciliation

At startup, storage reconciliation:

- Removes non-multipart files under `.tmp`.
- Walks bucket directories and deletes any backing file not referenced by object metadata.
- Prunes empty directories under bucket roots.
- Removes multipart temp directories that do not have corresponding upload IDs in SQLite.

This preserves the rule that SQLite metadata is the source of truth.

## Multipart Storage

Part uploads are written to unique files under:

```text
data/.tmp/multipart/{upload-id}/{part-number}.tmp-{uuid}
```

Re-uploading a part creates a new file and updates part metadata. The old part file is deleted after metadata update succeeds.

Completion assembles requested part files into a new temp file, syncs it, renames it to a UUID object path, and commits the object metadata atomically with upload deletion.

## Stale Multipart Cleanup

A background goroutine runs every configured cleanup interval. It:

- Lists stale uploads older than the configured TTL.
- Claims active stale uploads as `aborted` before deletion.
- Gives non-active uploads an extra TTL-sized grace period.
- Deletes upload metadata before deleting uploaded part files.
- Logs cleanup errors and continues.

Default TTL is `24h`; default cleanup interval is `1h`.

## Metadata Cache

When `FBS_METADATA_CACHE_SIZE` or `--metadata-cache-size` is greater than zero, bucket and object repositories are wrapped in a metadata cache. The default budget is 512 MiB. Set the budget to `0` to disable cache wrappers.

Cache invalidation is handled by repository wrappers and explicit multipart completion cache updates.

