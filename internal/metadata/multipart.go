package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)
// MultipartUpload represents a row in the multipart_uploads table.
type MultipartUpload struct {
	ID                string
	BucketName        string
	Key               string
	ContentType       string
	ChecksumAlgorithm string
	Status            string
	CreatedAt         time.Time
	StatusUpdatedAt   time.Time
	UserMetadata      map[string]string
}

// MultipartPart represents a row in the multipart_parts table.
type MultipartPart struct {
	UploadID         string
	PartNumber       int
	Size             int64
	ETag             string
	StoragePath      string
	CreatedAt        time.Time
	ChecksumCRC32    string
	ChecksumCRC32C   string
	ChecksumCRC64NVME string
	ChecksumSHA1     string
	ChecksumSHA256   string
}

// MultipartUploadRepository defines CRUD operations for multipart uploads.
type MultipartUploadRepository interface {
	Create(ctx context.Context, upload *MultipartUpload) error
	GetByID(ctx context.Context, id string) (*MultipartUpload, error)
	Delete(ctx context.Context, id string) error
	ListStale(ctx context.Context, olderThan time.Time) ([]MultipartUpload, error)
	// AddPart inserts or updates a part and returns the previous storage_path
	// (empty string if the part did not exist before). Uses a database-level
	// write lock for correctness across processes. Returns ErrUploadAlreadyClaimed
	// if the upload is no longer active.
	AddPart(ctx context.Context, part *MultipartPart) (oldStoragePath string, err error)
	ListParts(ctx context.Context, uploadID string) ([]MultipartPart, error)
	// ListAllUploadIDs returns every upload ID in the multipart_uploads table.
	ListAllUploadIDs(ctx context.Context) ([]string, error)
	// CompleteUpload atomically verifies the upload exists, creates/upserts the
	// object, deletes the upload, and returns the previous object storage_path
	// (empty string if the object did not exist before).
	// Returns ErrMultipartUploadNotFound if the upload does not exist.
	// Returns ErrPreconditionFailed if ifMatch/ifNoneMatch preconditions fail.
	// Returns ErrObjectNotFound if ifMatch is set but the object doesn't exist.
	CompleteUpload(ctx context.Context, obj *Object, uploadID string, ifMatch, ifNoneMatch string) (oldStoragePath string, err error)
	// ClaimUpload atomically checks the upload is active and sets its status.
	// Returns ErrMultipartUploadNotFound if the upload does not exist,
	// or ErrUploadAlreadyClaimed if it is no longer active.
	ClaimUpload(ctx context.Context, uploadID string, status string) error
	// SetUploadStatus unconditionally sets the upload status.
	// Returns ErrMultipartUploadNotFound if the upload does not exist.
	SetUploadStatus(ctx context.Context, uploadID string, status string) error
	// ListByBucket returns multipart uploads in a bucket, with optional filtering
	// and pagination. maxUploads is clamped to 1–1000.
	ListByBucket(ctx context.Context, bucketName string, prefix, keyMarker, uploadIDMarker string, maxUploads int) (uploads []MultipartUpload, isTruncated bool, nextKeyMarker string, nextUploadIDMarker string, err error)
}

// ErrMultipartUploadNotFound is returned when an upload lookup yields no rows.
var ErrMultipartUploadNotFound = errors.New("multipart upload not found")

// ErrUploadAlreadyClaimed is returned when an upload is no longer active.
var ErrUploadAlreadyClaimed = errors.New("multipart upload already claimed")

// ErrPreconditionFailed is returned when a precondition check fails.
var ErrPreconditionFailed = errors.New("precondition failed")

// Multipart upload status values.
const (
	MultipartUploadStatusActive     = "active"
	MultipartUploadStatusCompleting = "completing"
	MultipartUploadStatusAborted    = "aborted"
)

type sqliteMultipartUploadRepository struct {
	db *sql.DB
}

// NewMultipartUploadRepository returns a MultipartUploadRepository backed by the given *sql.DB.
func NewMultipartUploadRepository(db *sql.DB) MultipartUploadRepository {
	return &sqliteMultipartUploadRepository{db: db}
}

func (r *sqliteMultipartUploadRepository) Create(ctx context.Context, upload *MultipartUpload) error {
	const q = `
		INSERT INTO multipart_uploads (id, bucket_name, key, content_type, status, created_at, status_updated_at, checksum_algorithm, user_metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if upload.ContentType == "" {
		upload.ContentType = "application/octet-stream"
	}
	if upload.Status == "" {
		upload.Status = MultipartUploadStatusActive
	}
	if !validMultipartUploadStatus(upload.Status) {
		return fmt.Errorf("invalid multipart upload status: %s", upload.Status)
	}
	if upload.CreatedAt.IsZero() {
		upload.CreatedAt = time.Now().UTC()
	}
	if upload.StatusUpdatedAt.IsZero() {
		upload.StatusUpdatedAt = upload.CreatedAt
	}


	metaJSON, err := json.Marshal(upload.UserMetadata)
	if err != nil {
		return fmt.Errorf("marshal user metadata: %w", err)
	}

	_, err = r.db.ExecContext(ctx, q,
		upload.ID,
		upload.BucketName,
		upload.Key,
		upload.ContentType,
		upload.Status,
		upload.CreatedAt.UTC(),
		upload.StatusUpdatedAt.UTC(),
		upload.ChecksumAlgorithm,
		string(metaJSON),
	)
	if err != nil {
		return fmt.Errorf("create multipart upload: %w", err)
	}

	return nil
}

func (r *sqliteMultipartUploadRepository) GetByID(ctx context.Context, id string) (*MultipartUpload, error) {
	const q = `
		SELECT id, bucket_name, key, content_type, status, created_at, status_updated_at, checksum_algorithm, user_metadata
		FROM multipart_uploads
		WHERE id = ?`

	row := r.db.QueryRowContext(ctx, q, id)
	return scanMultipartUpload(row)
}

func (r *sqliteMultipartUploadRepository) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM multipart_uploads WHERE id = ?`

	result, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete multipart upload: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete multipart upload rows affected: %w", err)
	}
	if rows == 0 {
		return ErrMultipartUploadNotFound
	}

	return nil
}

func (r *sqliteMultipartUploadRepository) ListStale(ctx context.Context, olderThan time.Time) ([]MultipartUpload, error) {
	const q = `
		SELECT id, bucket_name, key, content_type, status, created_at, status_updated_at, checksum_algorithm, user_metadata
		FROM multipart_uploads
		WHERE (status = ? AND created_at < ?)
		   OR (status <> ? AND status_updated_at < ?)
		ORDER BY created_at ASC`

	cutoff := olderThan.UTC()
	rows, err := r.db.QueryContext(ctx, q, MultipartUploadStatusActive, cutoff, MultipartUploadStatusActive, cutoff)
	if err != nil {
		return nil, fmt.Errorf("list stale uploads: %w", err)
	}
	defer rows.Close()

	var uploads []MultipartUpload
	for rows.Next() {
		u, err := scanMultipartUploadRow(rows)
		if err != nil {
			return nil, fmt.Errorf("list stale uploads scan: %w", err)
		}
		uploads = append(uploads, *u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list stale uploads rows: %w", err)
	}

	return uploads, nil
}

func (r *sqliteMultipartUploadRepository) ListAllUploadIDs(ctx context.Context) ([]string, error) {
	const q = `SELECT id FROM multipart_uploads`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list all upload ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list all upload ids scan: %w", err)
		}
		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list all upload ids rows: %w", err)
	}

	return ids, nil
}

func (r *sqliteMultipartUploadRepository) ClaimUpload(ctx context.Context, uploadID string, status string) error {
	if !validMultipartUploadStatus(status) || status == MultipartUploadStatusActive {
		return fmt.Errorf("invalid multipart upload claim status: %s", status)
	}

	conn, err := r.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire db connection for claim upload: %w", err)
	}
	defer conn.Close()

	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate tx: %w", err)
	}

	var currentStatus string
	if err := conn.QueryRowContext(ctx,
		`SELECT status FROM multipart_uploads WHERE id = ?`,
		uploadID,
	).Scan(&currentStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMultipartUploadNotFound
		}
		return fmt.Errorf("select upload status: %w", err)
	}

	if currentStatus != MultipartUploadStatusActive {
		return ErrUploadAlreadyClaimed
	}

	if _, err := conn.ExecContext(ctx,
		`UPDATE multipart_uploads SET status = ?, status_updated_at = ? WHERE id = ?`,
		status, time.Now().UTC(), uploadID,
	); err != nil {
		return fmt.Errorf("update upload status: %w", err)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit claim upload tx: %w", err)
	}
	committed = true
	return nil
}

func (r *sqliteMultipartUploadRepository) SetUploadStatus(ctx context.Context, uploadID string, status string) error {
	if !validMultipartUploadStatus(status) {
		return fmt.Errorf("invalid multipart upload status: %s", status)
	}

	result, err := r.db.ExecContext(ctx,
		`UPDATE multipart_uploads SET status = ?, status_updated_at = ? WHERE id = ?`,
		status, time.Now().UTC(), uploadID,
	)
	if err != nil {
		return fmt.Errorf("set upload status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("set upload status rows affected: %w", err)
	}
	if rows == 0 {
		return ErrMultipartUploadNotFound
	}

	return nil
}

func (r *sqliteMultipartUploadRepository) AddPart(ctx context.Context, part *MultipartPart) (string, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire db connection for add part: %w", err)
	}
	defer conn.Close()

	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return "", fmt.Errorf("begin immediate tx: %w", err)
	}

	var status string
	if err := conn.QueryRowContext(ctx,
		`SELECT status FROM multipart_uploads WHERE id = ?`,
		part.UploadID,
	).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrMultipartUploadNotFound
		}
		return "", fmt.Errorf("select upload status: %w", err)
	}
	if status != MultipartUploadStatusActive {
		return "", ErrUploadAlreadyClaimed
	}

	var oldStoragePath string
	if err := conn.QueryRowContext(ctx,
		`SELECT storage_path FROM multipart_parts WHERE upload_id = ? AND part_number = ?`,
		part.UploadID, part.PartNumber,
	).Scan(&oldStoragePath); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("select existing part: %w", err)
	}

	const q = `
		INSERT INTO multipart_parts (upload_id, part_number, size, etag, storage_path, created_at,
			checksum_crc32, checksum_crc32c, checksum_crc64nvme, checksum_sha1, checksum_sha256)
		VALUES (?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?)
		ON CONFLICT(upload_id, part_number) DO UPDATE SET
			size = excluded.size,
			etag = excluded.etag,
			storage_path = excluded.storage_path,
			created_at = excluded.created_at,
			checksum_crc32 = excluded.checksum_crc32,
			checksum_crc32c = excluded.checksum_crc32c,
			checksum_crc64nvme = excluded.checksum_crc64nvme,
			checksum_sha1 = excluded.checksum_sha1,
			checksum_sha256 = excluded.checksum_sha256`

	if part.CreatedAt.IsZero() {
		part.CreatedAt = time.Now().UTC()
	}

	if _, err := conn.ExecContext(ctx, q,
		part.UploadID,
		part.PartNumber,
		part.Size,
		part.ETag,
		part.StoragePath,
		part.CreatedAt.UTC(),
		nullify(part.ChecksumCRC32),
		nullify(part.ChecksumCRC32C),
		nullify(part.ChecksumCRC64NVME),
		nullify(part.ChecksumSHA1),
		nullify(part.ChecksumSHA256),
	); err != nil {
		return "", fmt.Errorf("add multipart part: %w", err)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return "", fmt.Errorf("commit add part tx: %w", err)
	}
	committed = true
	return oldStoragePath, nil
}

func (r *sqliteMultipartUploadRepository) CompleteUpload(ctx context.Context, obj *Object, uploadID string, ifMatch, ifNoneMatch string) (string, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire db connection for complete upload: %w", err)
	}
	defer conn.Close()

	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return "", fmt.Errorf("begin immediate tx: %w", err)
	}

	var status string
	if err := conn.QueryRowContext(ctx,
		`SELECT status FROM multipart_uploads WHERE id = ?`,
		uploadID,
	).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrMultipartUploadNotFound
		}
		return "", fmt.Errorf("check upload status: %w", err)
	}
	if status != MultipartUploadStatusCompleting {
		return "", ErrUploadAlreadyClaimed
	}

	var oldStoragePath string
	var oldETag sql.NullString
	err = conn.QueryRowContext(ctx,
		`SELECT storage_path, etag FROM objects WHERE bucket_name = ? AND key = ?`,
		obj.BucketName, obj.Key,
	).Scan(&oldStoragePath, &oldETag)

	objectExists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("select existing object: %w", err)
	}

	// Check preconditions inside the transaction.
	if ifMatch != "" || ifNoneMatch != "" {
		if !objectExists && ifMatch != "" {
			// S3 returns NoSuchKey when If-Match is set and object doesn't exist.
			return "", ErrObjectNotFound
		}
		if objectExists {
			etag := oldETag.String
			if ifMatch != "" && ifMatch != "*" && !etagsEqual(ifMatch, etag) {
				return "", ErrPreconditionFailed
			}
			if ifNoneMatch == "*" {
				return "", ErrPreconditionFailed
			}
			if ifNoneMatch != "" && ifNoneMatch != "*" && etagsEqual(ifNoneMatch, etag) {
				return "", ErrPreconditionFailed
			}
		}
	}

	const createQ = `
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
			return "", fmt.Errorf("marshal user metadata: %w", err)
		}
		s := string(b)
		metaStr = &s
	}


	if _, err := conn.ExecContext(ctx, createQ,
		obj.ID, obj.BucketName, obj.Key, obj.Size, obj.ETag,
		obj.ContentType, obj.StoragePath, now, obj.UpdatedAt.UTC(),
		obj.IsMultipart, obj.PartsCount,
		nullify(obj.ChecksumCRC32), nullify(obj.ChecksumCRC32C), nullify(obj.ChecksumCRC64NVME),
		nullify(obj.ChecksumSHA1), nullify(obj.ChecksumSHA256),
		metaStr,
	); err != nil {
		return "", fmt.Errorf("create object: %w", err)
	}

	if _, err := conn.ExecContext(ctx,
		`DELETE FROM multipart_uploads WHERE id = ?`,
		uploadID,
	); err != nil {
		return "", fmt.Errorf("delete multipart upload: %w", err)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return "", fmt.Errorf("commit complete upload tx: %w", err)
	}
	committed = true
	return oldStoragePath, nil
}

func (r *sqliteMultipartUploadRepository) ListParts(ctx context.Context, uploadID string) ([]MultipartPart, error) {
	const q = `
		SELECT upload_id, part_number, size, etag, storage_path, created_at,
			checksum_crc32, checksum_crc32c, checksum_crc64nvme, checksum_sha1, checksum_sha256
		FROM multipart_parts
		WHERE upload_id = ?
		ORDER BY part_number ASC`

	rows, err := r.db.QueryContext(ctx, q, uploadID)
	if err != nil {
		return nil, fmt.Errorf("list multipart parts: %w", err)
	}
	defer rows.Close()

	var parts []MultipartPart
	for rows.Next() {
		p, err := scanMultipartPartRow(rows)
		if err != nil {
			return nil, fmt.Errorf("list multipart parts scan: %w", err)
		}
		parts = append(parts, *p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list multipart parts rows: %w", err)
	}

	return parts, nil
}

func (r *sqliteMultipartUploadRepository) ListByBucket(ctx context.Context, bucketName string, prefix, keyMarker, uploadIDMarker string, maxUploads int) ([]MultipartUpload, bool, string, string, error) {
	if maxUploads <= 0 || maxUploads > 1000 {
		maxUploads = 1000
	}

	const q = `
		SELECT id, bucket_name, key, content_type, status, created_at, status_updated_at, checksum_algorithm, user_metadata
		FROM multipart_uploads
		WHERE bucket_name = ?
		  AND status = 'active'
		  AND (? = '' OR substr(key, 1, length(?)) = ?)
		  AND (? = '' OR key > ? OR (key = ? AND ? != '' AND id > ?))
		ORDER BY key, id
		LIMIT ?`

	// We query maxUploads+1 to detect truncation.
	limit := maxUploads + 1

	rows, err := r.db.QueryContext(ctx, q, bucketName, prefix, prefix, prefix, keyMarker, keyMarker, keyMarker, uploadIDMarker, uploadIDMarker, limit)
	if err != nil {
		return nil, false, "", "", fmt.Errorf("list multipart uploads by bucket: %w", err)
	}
	defer rows.Close()

	var uploads []MultipartUpload
	for rows.Next() {
		u, err := scanMultipartUploadRow(rows)
		if err != nil {
			return nil, false, "", "", fmt.Errorf("list multipart uploads by bucket scan: %w", err)
		}
		uploads = append(uploads, *u)
	}

	if err := rows.Err(); err != nil {
		return nil, false, "", "", fmt.Errorf("list multipart uploads by bucket rows: %w", err)
	}

	isTruncated := len(uploads) > maxUploads
	if isTruncated {
		uploads = uploads[:maxUploads]
		last := uploads[len(uploads)-1]
		return uploads, true, last.Key, last.ID, nil
	}

	return uploads, false, "", "", nil
}

func scanMultipartUpload(row *sql.Row) (*MultipartUpload, error) {
	var u MultipartUpload
	var createdAt, statusUpdatedAt string
	var metaStr sql.NullString

	err := row.Scan(&u.ID, &u.BucketName, &u.Key, &u.ContentType, &u.Status, &createdAt, &statusUpdatedAt, &u.ChecksumAlgorithm, &metaStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMultipartUploadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan multipart upload: %w", err)
	}

	u.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, err
	}
	u.StatusUpdatedAt, err = parseTimestamp(statusUpdatedAt)
	if err != nil {
		return nil, err
	}

	if metaStr.Valid && metaStr.String != "" {
		if err := json.Unmarshal([]byte(metaStr.String), &u.UserMetadata); err != nil {
			return nil, fmt.Errorf("unmarshal user metadata: %w", err)
		}
	}

	return &u, nil
}

func scanMultipartUploadRow(rows *sql.Rows) (*MultipartUpload, error) {
	var u MultipartUpload
	var createdAt, statusUpdatedAt string
	var metaStr sql.NullString

	if err := rows.Scan(&u.ID, &u.BucketName, &u.Key, &u.ContentType, &u.Status, &createdAt, &statusUpdatedAt, &u.ChecksumAlgorithm, &metaStr); err != nil {
		return nil, fmt.Errorf("scan multipart upload row: %w", err)
	}

	var err error
	u.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, err
	}
	u.StatusUpdatedAt, err = parseTimestamp(statusUpdatedAt)
	if err != nil {
		return nil, err
	}

	if metaStr.Valid && metaStr.String != "" {
		if err := json.Unmarshal([]byte(metaStr.String), &u.UserMetadata); err != nil {
			return nil, fmt.Errorf("unmarshal user metadata: %w", err)
		}
	}

	return &u, nil
}



func scanMultipartPartRow(rows *sql.Rows) (*MultipartPart, error) {
	var p MultipartPart
	var createdAt string
	var csCRC32, csCRC32C, csCRC64NVME, csSHA1, csSHA256 sql.NullString

	if err := rows.Scan(&p.UploadID, &p.PartNumber, &p.Size, &p.ETag, &p.StoragePath, &createdAt,
		&csCRC32, &csCRC32C, &csCRC64NVME, &csSHA1, &csSHA256,
	); err != nil {
		return nil, fmt.Errorf("scan multipart part row: %w", err)
	}

	p.ChecksumCRC32 = csCRC32.String
	p.ChecksumCRC32C = csCRC32C.String
	p.ChecksumCRC64NVME = csCRC64NVME.String
	p.ChecksumSHA1 = csSHA1.String
	p.ChecksumSHA256 = csSHA256.String

	var err error
	p.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, err
	}

	return &p, nil
}

func validMultipartUploadStatus(status string) bool {
	switch status {
	case MultipartUploadStatusActive, MultipartUploadStatusCompleting, MultipartUploadStatusAborted:
		return true
	default:
		return false
	}
}

// nullify returns nil for empty strings so they are stored as NULL in the database.
func nullify(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// unquoteETag removes surrounding double quotes from an ETag if present.
func unquoteETag(etag string) string {
	if len(etag) >= 2 && etag[0] == '"' && etag[len(etag)-1] == '"' {
		return etag[1 : len(etag)-1]
	}
	return etag
}

// etagsEqual performs case-insensitive comparison of two ETags after unquoting.
func etagsEqual(a, b string) bool {
	return strings.EqualFold(unquoteETag(a), unquoteETag(b))
}
