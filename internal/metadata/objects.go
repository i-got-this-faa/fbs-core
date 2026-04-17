package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Object represents a row in the objects table.
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

// ObjectRepository defines CRUD operations for objects.
type ObjectRepository interface {
	Create(ctx context.Context, obj *Object) error
	GetByKey(ctx context.Context, bucketName, key string) (*Object, error)
	List(ctx context.Context, bucketName, prefix, startAfter string, maxKeys int) ([]Object, bool, error)
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
		INSERT INTO objects (id, bucket_name, key, size, etag, content_type, storage_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(bucket_name, key) DO UPDATE SET
			id = excluded.id,
			size = excluded.size,
			etag = excluded.etag,
			content_type = excluded.content_type,
			storage_path = excluded.storage_path,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at`

	if obj.CreatedAt.IsZero() {
		obj.CreatedAt = time.Now().UTC()
	}
	if obj.UpdatedAt.IsZero() {
		obj.UpdatedAt = obj.CreatedAt
	}
	now := obj.CreatedAt.UTC()

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
	)
	if err != nil {
		return fmt.Errorf("create object: %w", err)
	}

	return nil
}

func (r *sqliteObjectRepository) GetByKey(ctx context.Context, bucketName, key string) (*Object, error) {
	const q = `
		SELECT id, bucket_name, key, size, etag, content_type, storage_path, created_at, updated_at
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
		SELECT id, bucket_name, key, size, etag, content_type, storage_path, created_at, updated_at
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

	err := row.Scan(&o.ID, &o.BucketName, &o.Key, &o.Size, &o.ETag, &o.ContentType, &o.StoragePath, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan object: %w", err)
	}

	o.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, err
	}
	o.UpdatedAt, err = parseTimestamp(updatedAt)
	if err != nil {
		return nil, err
	}

	return &o, nil
}

func scanObjectRow(rows *sql.Rows) (*Object, error) {
	var o Object
	var createdAt, updatedAt string

	if err := rows.Scan(&o.ID, &o.BucketName, &o.Key, &o.Size, &o.ETag, &o.ContentType, &o.StoragePath, &createdAt, &updatedAt); err != nil {
		return nil, fmt.Errorf("scan object row: %w", err)
	}

	var err error
	o.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, err
	}
	o.UpdatedAt, err = parseTimestamp(updatedAt)
	if err != nil {
		return nil, err
	}

	return &o, nil
}
