# F3: Disk Storage Engine (Backend)

**Status:** TODO

## Summary

Implement the file system layer that handles all physical data I/O for object storage. This engine manages atomic writes, direct reads, soft deletes, path sanitization, and startup reconciliation against the SQLite metadata layer (F2).

## Scope

- File system operations matching the `/data/{bucket_name}/{object_key}` structure
- Atomic writes: upload to `.tmp/UUID`, then rename on success
- Direct file reads and soft delete
- Path sanitization: reject traversal attacks, null bytes, invalid characters; create intermediate directories
- Startup reconciliation: purge `.tmp/` and remove orphaned data files with no SQLite metadata row

## Prerequisites

- **F1 (Core HTTP Server & Routing):** DONE - provides `config.Config` to extend with data directory setting
- **F2 (SQLite Metadata Layer):** TODO - provides `ObjectRepository` for reconciliation and the rule that SQLite is the source of truth

## Implementation Details

### Configuration Extension

Extend `config.Config` with:

| Setting | Flag | Env Var | Default |
|---|---|---|---|
| Data root directory | `--data-dir` | `FBS_DATA_DIR` | `./data` |

Validation: directory must be writable. On startup, create it (and `.tmp/` inside it) if it doesn't exist.

### Directory Layout

```
<data-dir>/
  .tmp/                              # Staging area for atomic writes
    <uuid>.tmp                        # In-flight uploads
  <bucket_name>/
    <object_key>                      # Final object data
    nested/path/to/object             # Keys with '/' create subdirectories
```

### Core Interface

```go
// internal/storage/engine.go

type DiskEngine interface {
    // Write atomically stores data for a given bucket/key.
    // Data is written to .tmp/UUID first, then renamed to final path.
    // Returns the relative storage path and number of bytes written.
    Write(ctx context.Context, bucketName, key string, r io.Reader) (storagePath string, size int64, err error)

    // Read opens the file at the given storage path for reading.
    // Caller is responsible for closing the returned ReadCloser.
    Read(ctx context.Context, storagePath string) (io.ReadCloser, error)

    // Open returns an *os.File for the given storage path (needed for http.ServeContent in F9).
    Open(ctx context.Context, storagePath string) (*os.File, error)

    // Delete removes the data file at the given storage path.
    // Returns nil if the file doesn't exist (idempotent).
    Delete(ctx context.Context, storagePath string) error

    // Reconcile purges .tmp/ and removes orphaned data files.
    // Called once at startup.
    Reconcile(ctx context.Context, knownObjects func(bucketName string) ([]string, error)) error

    // StoragePath computes the relative storage path for a bucket/key pair.
    StoragePath(bucketName, key string) string
}
```

### Atomic Write Flow

The write-before-metadata ordering is critical for data integrity (per SPEC.md Section 4):

```
1. Sanitize the object key (reject invalid paths)
2. Generate a UUID for the temp file
3. Create temp file: <data-dir>/.tmp/<uuid>.tmp
4. Copy data from io.Reader to temp file
5. Sync (fsync) the temp file to ensure durability
6. Close the temp file
7. Compute the final path: <data-dir>/<bucket_name>/<object_key>
8. Create intermediate directories for the final path (mkdir -p)
9. Rename (atomic move) temp file to final path
10. Return storage path and bytes written
    -- Caller (F6) then inserts metadata into SQLite --
```

If any step fails (write error, disk full, etc.):
- Remove the temp file (best effort cleanup)
- Return the error; no metadata is ever inserted for a failed write

### Path Sanitization

Implement `ValidateKey(key string) error` that rejects:

| Rule | Reason |
|---|---|
| Empty key | S3 requires a non-empty key |
| Contains `../` or `..\\` | Path traversal attack |
| Contains null byte (`\x00`) | Filesystem injection |
| Contains `\r`, `\n` | HTTP header injection via key reflection |
| Starts with `/` | Ambiguous absolute path |
| Key resolves outside data root after `filepath.Clean` | Defense-in-depth traversal check |
| Key length > 1024 characters | S3 key limit |
| Contains characters invalid on host filesystem | OS-specific (`:`, `*`, `?`, `"`, `<`, `>`, `\|` on Windows; generally permissive on Linux but still reject null) |

After validation, `filepath.Clean` the key and resolve it against `<data-dir>/<bucket_name>/` to get the absolute final path. Verify the result is still under the data root (defense-in-depth).

### Intermediate Directory Creation

Object keys containing `/` (e.g., `photos/2024/image.jpg`) require creating parent directories:

```go
finalDir := filepath.Dir(finalPath)
if err := os.MkdirAll(finalDir, 0o755); err != nil {
    return "", 0, fmt.Errorf("create directories for key: %w", err)
}
```

### Direct File Read

```go
func (e *engine) Read(ctx context.Context, storagePath string) (io.ReadCloser, error) {
    fullPath := filepath.Join(e.dataDir, storagePath)
    // Verify path is under data root (defense-in-depth)
    f, err := os.Open(fullPath)
    if errors.Is(err, os.ErrNotExist) {
        return nil, ErrNotFound
    }
    return f, err
}
```

For F9's zero-copy optimization, `Open()` returns `*os.File` directly so `http.ServeContent` can use `sendfile()`.

### Soft Delete

```go
func (e *engine) Delete(ctx context.Context, storagePath string) error {
    fullPath := filepath.Join(e.dataDir, storagePath)
    err := os.Remove(fullPath)
    if errors.Is(err, os.ErrNotExist) {
        return nil // idempotent
    }
    // Optionally clean up empty parent directories
    e.pruneEmptyParents(fullPath)
    return err
}
```

After deleting a file, walk up the directory tree and remove empty parent directories up to (but not including) the bucket root. This prevents accumulation of empty directory trees from deleted objects with nested keys.

### Startup Reconciliation

Called once from `main.go` before the server starts accepting traffic:

```
Phase 1: Purge .tmp/
  - Walk <data-dir>/.tmp/
  - Delete all files (these are incomplete uploads from a previous crash)
  - Log count of purged files

Phase 2: Remove orphaned data files
  - Walk each <data-dir>/<bucket_name>/ directory
  - For each file, check if a corresponding metadata row exists in SQLite (via ObjectRepository)
  - If no metadata row exists, delete the file (it's an orphan from a crash after write but before metadata commit, or from a metadata deletion that failed to clean up the file)
  - Log count of orphaned files removed

Phase 3: Prune empty directories
  - Remove any empty directories left behind after orphan cleanup
```

The `knownObjects` callback queries F2's `ObjectRepository` to list all known storage paths for a given bucket, avoiding tight coupling to the database layer.

### Error Types

```go
var (
    ErrNotFound       = errors.New("storage: file not found")
    ErrInvalidKey     = errors.New("storage: invalid object key")
    ErrPathTraversal  = errors.New("storage: path traversal detected")
)
```

### Package Structure

```
internal/storage/
  engine.go       # DiskEngine interface, constructor, config
  write.go        # Atomic write implementation
  read.go         # Read and Open implementations
  delete.go       # Delete and empty directory pruning
  sanitize.go     # ValidateKey, path resolution, defense-in-depth checks
  reconcile.go    # Startup reconciliation logic
  errors.go       # Sentinel error definitions
```

## Test Plan

| Test | What It Verifies |
|---|---|
| `TestAtomicWrite` | Data written to `.tmp/` first, renamed to final path; final file contains correct data |
| `TestAtomicWriteFailure` | If write fails mid-stream, temp file is cleaned up and no final file exists |
| `TestWriteCreatesIntermediateDirectories` | Key `a/b/c/file.txt` creates `a/b/c/` directory tree under bucket |
| `TestRead` | Written file can be read back with identical contents |
| `TestReadNotFound` | Reading a non-existent path returns `ErrNotFound` |
| `TestOpen` | Returns `*os.File` suitable for `http.ServeContent` |
| `TestDeleteExisting` | File is removed from disk |
| `TestDeleteIdempotent` | Deleting a non-existent file returns nil (no error) |
| `TestDeletePrunesEmptyParents` | After deleting `a/b/c/file.txt`, empty dirs `c/`, `b/`, `a/` are removed |
| `TestValidateKey_ValidKeys` | Accepts: `file.txt`, `path/to/file`, `file-name_v2.tar.gz`, unicode keys |
| `TestValidateKey_Rejects` | Rejects: empty, `../etc/passwd`, `foo/../../bar`, keys with null bytes, keys starting with `/` |
| `TestPathResolutionDefenseInDepth` | Even if `filepath.Clean` produces a path outside data root, it is rejected |
| `TestReconcilePurgesTmp` | Files in `.tmp/` are deleted on reconciliation |
| `TestReconcileRemovesOrphans` | Files on disk with no matching metadata row are deleted |
| `TestReconcileKeepsValidFiles` | Files with corresponding metadata rows are preserved |
| `TestStoragePath` | Returns the expected `<bucket>/<key>` relative path |

All tests should use `t.TempDir()` for isolation.

## Dependencies

- Go stdlib only: `os`, `io`, `path/filepath`, `errors`, `fmt`, `context`
- `github.com/google/uuid` - temp file naming

## Interfaces Provided to Other Features

- **`DiskEngine`** - consumed by F6 (PUT/GET/DELETE write/read/delete data), F8 (multipart part storage), F9 (Open for zero-copy sendfile)
- **`ValidateKey()`** - used by F6 to reject bad keys before any I/O
- **`Reconcile()`** - called from `main.go` at startup, queries F2's repository interfaces
- **`StoragePath()`** - used by F6 to compute the path stored in the `objects.storage_path` column (F2)
