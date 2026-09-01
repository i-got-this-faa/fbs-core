package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Object struct {
	ID                string
	BucketName        string
	Key               string
	Size              int64
	ETag              string
	ContentType       string
	StoragePath       string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	IsMultipart       bool
	PartsCount        int
	ChecksumCRC32     string
	ChecksumCRC32C    string
	ChecksumCRC64NVME string
	ChecksumSHA1      string
	ChecksumSHA256    string
	UserMetadata      map[string]string
}

// DelimitedListEntry is one result row from ListDelimited. VirtualKey holds
// the grouped key: for direct objects it equals the object key; for common
// prefixes it is the prefix string (ends with the delimiter). CursorKey is
// the last underlying object key in the group and is used as the continuation
// cursor. Object is populated only when IsPrefix is false.
type DelimitedListEntry struct {
	VirtualKey string
	CursorKey  string
	IsPrefix   bool
	Object     *Object
}

// ObjectRepository defines CRUD operations for objects.
type ObjectRepository interface {
	Create(ctx context.Context, obj *Object) error
	GetByKey(ctx context.Context, bucketName, key string) (*Object, error)
	List(ctx context.Context, bucketName, prefix, startAfter string, maxKeys int) ([]Object, bool, error)
	// ListDelimited groups keys by their immediate virtual prefix under
	// delimiter, returning at most maxKeys+1 entries so callers can detect
	// truncation by checking len > maxKeys. Common-prefix entries have
	// IsPrefix=true and nil Object; direct-object entries have IsPrefix=false
	// and a populated Object.
	ListDelimited(ctx context.Context, bucketName, prefix, startAfter, delimiter string, maxKeys int) ([]DelimitedListEntry, error)
	Delete(ctx context.Context, bucketName, key string) error
	DeleteAllInBucket(ctx context.Context, bucketName string) error
}

// ErrObjectNotFound is returned when an object lookup yields no rows.
var ErrObjectNotFound = errors.New("object not found")

type sqliteObjectRepository struct {
	db *sql.DB
}

// NewObjectRepository returns a ObjectRepository backed by the given *sql.DB.
func NewObjectRepository(db *sql.DB) ObjectRepository {
	return &sqliteObjectRepository{db: db}
}
func (r *sqliteObjectRepository) Create(ctx context.Context, obj *Object) error {
	const q = `
		INSERT INTO objects (id, bucket_name, key, size, etag, content_type, storage_path, created_at, updated_at,
			is_multipart, parts_count, checksum_crc32, checksum_crc32c, checksum_crc64nvme, checksum_sha1, checksum_sha256, user_metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(bucket_name, key) DO UPDATE SET
			id = excluded.id,
			size = excluded.size,
			etag = excluded.etag,
			content_type = excluded.content_type,
			storage_path = excluded.storage_path,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			is_multipart = excluded.is_multipart,
			parts_count = excluded.parts_count,
			checksum_crc32 = excluded.checksum_crc32,
			checksum_crc32c = excluded.checksum_crc32c,
			checksum_crc64nvme = excluded.checksum_crc64nvme,
			checksum_sha1 = excluded.checksum_sha1,
			checksum_sha256 = excluded.checksum_sha256,
			user_metadata = excluded.user_metadata`

	if obj.CreatedAt.IsZero() {
		obj.CreatedAt = time.Now().UTC()
	}
	if obj.UpdatedAt.IsZero() {
		obj.UpdatedAt = obj.CreatedAt
	}
	now := obj.CreatedAt.UTC()

	var metaStr *string
	if len(obj.UserMetadata) > 0 {
		b, err := json.Marshal(obj.UserMetadata)
		if err != nil {
			return fmt.Errorf("marshal user metadata: %w", err)
		}
		s := string(b)
		metaStr = &s
	}

	_, err := r.db.ExecContext(ctx, q,
		obj.ID,
		obj.BucketName,
		obj.Key,
		obj.Size,
		obj.ETag,
		obj.ContentType,
		obj.StoragePath,
		now,
		obj.UpdatedAt.UTC(),
		obj.IsMultipart,
		obj.PartsCount,
		obj.ChecksumCRC32,
		obj.ChecksumCRC32C,
		obj.ChecksumCRC64NVME,
		obj.ChecksumSHA1,
		obj.ChecksumSHA256,
		metaStr,
	)
	if err != nil {
		return fmt.Errorf("create object: %w", err)
	}

	return nil
}

func (r *sqliteObjectRepository) GetByKey(ctx context.Context, bucketName, key string) (*Object, error) {
	const q = `
		SELECT id, bucket_name, key, size, etag, content_type, storage_path, created_at, updated_at,
			is_multipart, parts_count, checksum_crc32, checksum_crc32c, checksum_crc64nvme, checksum_sha1, checksum_sha256, user_metadata
		FROM objects
		WHERE bucket_name = ? AND key = ?`

	row := r.db.QueryRowContext(ctx, q, bucketName, key)
	return scanObject(row)
}

func (r *sqliteObjectRepository) List(ctx context.Context, bucketName, prefix, startAfter string, maxKeys int) ([]Object, bool, error) {
	if maxKeys <= 0 {
		return []Object{}, false, nil
	}
	q := `
		SELECT id, bucket_name, key, size, etag, content_type, storage_path, created_at, updated_at,
			is_multipart, parts_count, checksum_crc32, checksum_crc32c, checksum_crc64nvme, checksum_sha1, checksum_sha256, user_metadata
		FROM objects
		WHERE bucket_name = ? AND key > ?`
	args := []any{bucketName, startAfter}
	if prefix != "" {
		q += ` AND substr(key, 1, length(?)) = ?`
		args = append(args, prefix, prefix)
	}
	q += ` ORDER BY key ASC LIMIT ?`

	limit := maxKeys + 1
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, fmt.Errorf("list objects: %w", err)
	}
	defer rows.Close()

	var objects []Object
	for rows.Next() {
		obj, err := scanObjectRow(rows)
		if err != nil {
			return nil, false, fmt.Errorf("list objects scan: %w", err)
		}
		objects = append(objects, *obj)
	}

	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list objects rows: %w", err)
	}

	isTruncated := false
	if len(objects) > maxKeys {
		isTruncated = true
		objects = objects[:maxKeys]
	}

	return objects, isTruncated, nil
}

func (r *sqliteObjectRepository) ListDelimited(ctx context.Context, bucketName, prefix, startAfter, delimiter string, maxKeys int) ([]DelimitedListEntry, error) {
	if maxKeys <= 0 {
		return nil, nil
	}

	// The query groups keys by their "virtual key": the portion of the key up
	// to and including the first delimiter after the prefix. Keys with no
	// delimiter in the remainder are their own virtual key (direct objects).
	// Keys that share a virtual key under a delimiter collapse into one
	// CommonPrefix entry whose cursor is MAX(key) within the group.
	//
	// Parameters (14 total, in order):
	//   raw CTE : prefixLen, bucketName, startAfter, prefix, prefixLen, prefix
	//   v CTE   : delimiter, delimiter, prefixLen, delimiter (virtual_key CASE)
	//             delimiter, delimiter                       (is_cp CASE)
	//   JOIN    : bucketName
	//   LIMIT   : maxKeys+1
	const q = `
WITH raw AS (
  SELECT key, substr(key, ?+1) AS remainder
  FROM objects
  WHERE bucket_name = ? AND key > ?
    AND (? = '' OR substr(key, 1, ?) = ?)
),
v AS (
  SELECT key,
    CASE WHEN ? != '' AND instr(remainder, ?) > 0
         THEN substr(key, 1, ? + instr(remainder, ?))
         ELSE key END AS virtual_key,
    CASE WHEN ? != '' AND instr(remainder, ?) > 0 THEN 1 ELSE 0 END AS is_cp
  FROM raw
),
grouped AS (
  SELECT virtual_key, max(key) AS cursor_key, max(is_cp) AS is_cp
  FROM v GROUP BY virtual_key
)
SELECT g.virtual_key, g.cursor_key, g.is_cp,
       o.id, o.bucket_name, o.key, o.size, o.etag, o.content_type, o.storage_path, o.created_at, o.updated_at
FROM grouped g
LEFT JOIN objects o ON g.is_cp = 0 AND o.bucket_name = ? AND o.key = g.virtual_key
ORDER BY g.virtual_key
LIMIT ?`

	prefixLen := len(prefix)
	args := []any{
		prefixLen, bucketName, startAfter, prefix, prefixLen, prefix, // raw CTE
		delimiter, delimiter, prefixLen, delimiter, // v CTE virtual_key
		delimiter, delimiter, // v CTE is_cp
		bucketName,  // JOIN
		maxKeys + 1, // LIMIT
	}

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list delimited objects: %w", err)
	}
	defer rows.Close()

	var entries []DelimitedListEntry
	for rows.Next() {
		var (
			virtualKey, cursorKey      string
			isCp                       int
			id, bName, key, etag       sql.NullString
			contentType, storagePath   sql.NullString
			createdAtStr, updatedAtStr sql.NullString
			size                       sql.NullInt64
		)
		if err := rows.Scan(
			&virtualKey, &cursorKey, &isCp,
			&id, &bName, &key, &size, &etag, &contentType, &storagePath, &createdAtStr, &updatedAtStr,
		); err != nil {
			return nil, fmt.Errorf("list delimited objects scan: %w", err)
		}

		entry := DelimitedListEntry{
			VirtualKey: virtualKey,
			CursorKey:  cursorKey,
			IsPrefix:   isCp != 0,
		}
		if isCp == 0 {
			obj := &Object{
				ID:          id.String,
				BucketName:  bName.String,
				Key:         key.String,
				Size:        size.Int64,
				ETag:        etag.String,
				ContentType: contentType.String,
				StoragePath: storagePath.String,
			}
			var parseErr error
			obj.CreatedAt, parseErr = parseTimestamp(createdAtStr.String)
			if parseErr != nil {
				return nil, parseErr
			}
			obj.UpdatedAt, parseErr = parseTimestamp(updatedAtStr.String)
			if parseErr != nil {
				return nil, parseErr
			}
			entry.Object = obj
		}
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list delimited objects rows: %w", err)
	}

	return entries, nil
}

func (r *sqliteObjectRepository) Delete(ctx context.Context, bucketName, key string) error {
	const q = `DELETE FROM objects WHERE bucket_name = ? AND key = ?`

	result, err := r.db.ExecContext(ctx, q, bucketName, key)
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete object rows affected: %w", err)
	}
	if rows == 0 {
		return ErrObjectNotFound
	}

	return nil
}

func (r *sqliteObjectRepository) DeleteAllInBucket(ctx context.Context, bucketName string) error {
	const q = `DELETE FROM objects WHERE bucket_name = ?`
	_, err := r.db.ExecContext(ctx, q, bucketName)
	if err != nil {
		return fmt.Errorf("delete all objects in bucket: %w", err)
	}
	return nil
}

func scanObject(row *sql.Row) (*Object, error) {
	var o Object
	var createdAt, updatedAt string
	var metaStr sql.NullString
	var csCRC32, csCRC32C, csCRC64NVME, csSHA1, csSHA256 sql.NullString

	err := row.Scan(&o.ID, &o.BucketName, &o.Key, &o.Size, &o.ETag, &o.ContentType, &o.StoragePath, &createdAt, &updatedAt,
		&o.IsMultipart, &o.PartsCount, &csCRC32, &csCRC32C, &csCRC64NVME, &csSHA1, &csSHA256, &metaStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan object: %w", err)
	}

	if err := applyScannedExtras(&o, createdAt, updatedAt, metaStr, csCRC32, csCRC32C, csCRC64NVME, csSHA1, csSHA256); err != nil {
		return nil, err
	}

	return &o, nil
}
func applyScannedExtras(o *Object, createdAt, updatedAt string, metaStr sql.NullString, csCRC32, csCRC32C, csCRC64NVME, csSHA1, csSHA256 sql.NullString) error {
	o.ChecksumCRC32 = csCRC32.String
	o.ChecksumCRC32C = csCRC32C.String
	o.ChecksumCRC64NVME = csCRC64NVME.String
	o.ChecksumSHA1 = csSHA1.String
	o.ChecksumSHA256 = csSHA256.String

	var err error
	o.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return err
	}
	o.UpdatedAt, err = parseTimestamp(updatedAt)
	if err != nil {
		return err
	}

	if metaStr.Valid && metaStr.String != "" {
		if err := json.Unmarshal([]byte(metaStr.String), &o.UserMetadata); err != nil {
			return fmt.Errorf("unmarshal user metadata: %w", err)
		}
	}
	return nil
}

func scanObjectRow(rows *sql.Rows) (*Object, error) {
	var o Object
	var createdAt, updatedAt string
	var metaStr sql.NullString
	var csCRC32, csCRC32C, csCRC64NVME, csSHA1, csSHA256 sql.NullString

	if err := rows.Scan(&o.ID, &o.BucketName, &o.Key, &o.Size, &o.ETag, &o.ContentType, &o.StoragePath, &createdAt, &updatedAt,
		&o.IsMultipart, &o.PartsCount, &csCRC32, &csCRC32C, &csCRC64NVME, &csSHA1, &csSHA256, &metaStr); err != nil {
		return nil, fmt.Errorf("scan object row: %w", err)
	}

	if err := applyScannedExtras(&o, createdAt, updatedAt, metaStr, csCRC32, csCRC32C, csCRC64NVME, csSHA1, csSHA256); err != nil {
		return nil, err
	}

	return &o, nil
}
