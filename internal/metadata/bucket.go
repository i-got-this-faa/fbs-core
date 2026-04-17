package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Bucket represents a row in the buckets table.
type Bucket struct {
	Name      string
	OwnerID   string
	CreatedAt time.Time
}

// BucketRepository defines CRUD operations for buckets.
type BucketRepository interface {
	Create(ctx context.Context, bucket *Bucket) error
	GetByName(ctx context.Context, name string) (*Bucket, error)
	List(ctx context.Context) ([]Bucket, error)
	Delete(ctx context.Context, name string) error
}

// ErrBucketNotFound is returned when a bucket lookup yields no rows.
var ErrBucketNotFound = errors.New("bucket not found")

type sqliteBucketRepository struct {
	db *sql.DB
}

// ////-7
// NewBucketRepository returns a BucketRepository backed by the given *sql.DB.
func NewBucketRepository(db *sql.DB) BucketRepository {
	return &sqliteBucketRepository{db: db}
}

func (r *sqliteBucketRepository) Create(ctx context.Context, bucket *Bucket) error {
	const q = `
		INSERT INTO buckets (name, owner_id, created_at)
		VALUES (?, ?, ?)`

	_, err := r.db.ExecContext(ctx, q,
		bucket.Name,
		bucket.OwnerID,
		bucket.CreatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("create bucket: %w", err)
	}

	return nil
}

func (r *sqliteBucketRepository) GetByName(ctx context.Context, name string) (*Bucket, error) {
	const q = `
		SELECT name, owner_id, created_at
		FROM buckets
		WHERE name = ?`

	row := r.db.QueryRowContext(ctx, q, name)
	return scanBucket(row)
}

func (r *sqliteBucketRepository) List(ctx context.Context) ([]Bucket, error) {
	const q = `
		SELECT name, owner_id, created_at
		FROM buckets
		ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list buckets: %w", err)
	}
	defer rows.Close()

	var buckets []Bucket
	for rows.Next() {
		b, err := scanBucketRow(rows)
		if err != nil {
			return nil, fmt.Errorf("list buckets scan: %w", err)
		}
		buckets = append(buckets, *b)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list buckets rows: %w", err)
	}

	return buckets, nil
}

func (r *sqliteBucketRepository) Delete(ctx context.Context, name string) error {
	const q = `DELETE FROM buckets WHERE name = ?`

	result, err := r.db.ExecContext(ctx, q, name)
	if err != nil {
		return fmt.Errorf("delete bucket: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete bucket rows affected: %w", err)
	}
	if rows == 0 {
		return ErrBucketNotFound
	}

	return nil
}

// scanBucket scans a single *sql.Row into a Bucket.
func scanBucket(row *sql.Row) (*Bucket, error) {
	var b Bucket
	var createdAt string

	err := row.Scan(&b.Name, &b.OwnerID, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBucketNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan bucket: %w", err)
	}

	b.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, err
	}

	return &b, nil
}

// scanBucketRow scans a *sql.Rows (multi-row query) into a Bucket.
func scanBucketRow(rows *sql.Rows) (*Bucket, error) {
	var b Bucket
	var createdAt string

	if err := rows.Scan(&b.Name, &b.OwnerID, &createdAt); err != nil {
		return nil, fmt.Errorf("scan bucket row: %w", err)
	}

	var err error
	b.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, err
	}

	return &b, nil
}
