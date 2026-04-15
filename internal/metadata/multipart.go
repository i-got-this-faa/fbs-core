package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MultipartUpload represents a row in the multipart_uploads table.
type MultipartUpload struct {
	ID         string
	BucketName string
	Key        string
	CreatedAt  time.Time
}

// MultipartPart represents a row in the multipart_parts table.
type MultipartPart struct {
	UploadID    string
	PartNumber  int
	Size        int64
	ETag        string
	StoragePath string
	CreatedAt   time.Time
}

// MultipartUploadRepository defines CRUD operations for multipart uploads.
type MultipartUploadRepository interface {
	Create(ctx context.Context, upload *MultipartUpload) error
	GetByID(ctx context.Context, id string) (*MultipartUpload, error)
	Delete(ctx context.Context, id string) error
	ListStale(ctx context.Context, olderThan time.Time) ([]MultipartUpload, error)
	AddPart(ctx context.Context, part *MultipartPart) error
	ListParts(ctx context.Context, uploadID string) ([]MultipartPart, error)
}

// ErrMultipartUploadNotFound is returned when an upload lookup yields no rows.
var ErrMultipartUploadNotFound = errors.New("multipart upload not found")

type sqliteMultipartUploadRepository struct {
	db *sql.DB
}

// NewMultipartUploadRepository returns a MultipartUploadRepository backed by the given *sql.DB.
func NewMultipartUploadRepository(db *sql.DB) MultipartUploadRepository {
	return &sqliteMultipartUploadRepository{db: db}
}

func (r *sqliteMultipartUploadRepository) Create(ctx context.Context, upload *MultipartUpload) error {
	const q = `
		INSERT INTO multipart_uploads (id, bucket_name, key, created_at)
		VALUES (?, ?, ?, ?)`

	_, err := r.db.ExecContext(ctx, q,
		upload.ID,
		upload.BucketName,
		upload.Key,
		upload.CreatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("create multipart upload: %w", err)
	}

	return nil
}

func (r *sqliteMultipartUploadRepository) GetByID(ctx context.Context, id string) (*MultipartUpload, error) {
	const q = `
		SELECT id, bucket_name, key, created_at
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
		SELECT id, bucket_name, key, created_at
		FROM multipart_uploads
		WHERE created_at < ?
		ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, q, olderThan.UTC())
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

func (r *sqliteMultipartUploadRepository) AddPart(ctx context.Context, part *MultipartPart) error {
	const q = `
		INSERT INTO multipart_parts (upload_id, part_number, size, etag, storage_path, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`

	_, err := r.db.ExecContext(ctx, q,
		part.UploadID,
		part.PartNumber,
		part.Size,
		part.ETag,
		part.StoragePath,
		part.CreatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("add multipart part: %w", err)
	}

	return nil
}

func (r *sqliteMultipartUploadRepository) ListParts(ctx context.Context, uploadID string) ([]MultipartPart, error) {
	const q = `
		SELECT upload_id, part_number, size, etag, storage_path, created_at
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

func scanMultipartUpload(row *sql.Row) (*MultipartUpload, error) {
	var u MultipartUpload
	var createdAt string

	err := row.Scan(&u.ID, &u.BucketName, &u.Key, &createdAt)
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

	return &u, nil
}

func scanMultipartUploadRow(rows *sql.Rows) (*MultipartUpload, error) {
	var u MultipartUpload
	var createdAt string

	if err := rows.Scan(&u.ID, &u.BucketName, &u.Key, &createdAt); err != nil {
		return nil, fmt.Errorf("scan multipart upload row: %w", err)
	}

	var err error
	u.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, err
	}

	return &u, nil
}

func scanMultipartPartRow(rows *sql.Rows) (*MultipartPart, error) {
	var p MultipartPart
	var createdAt string

	if err := rows.Scan(&p.UploadID, &p.PartNumber, &p.Size, &p.ETag, &p.StoragePath, &createdAt); err != nil {
		return nil, fmt.Errorf("scan multipart part row: %w", err)
	}

	var err error
	p.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, err
	}

	return &p, nil
}
