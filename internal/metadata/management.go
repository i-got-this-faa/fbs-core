package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type ManagementMetrics struct {
	BucketCount      int64
	ObjectCount      int64
	TotalObjectBytes int64
	UserCount        int64
	ActiveUserCount  int64
}

type BucketSummary struct {
	Name             string
	OwnerID          string
	CreatedAt        time.Time
	ObjectCount      int64
	TotalObjectBytes int64
}

type ManagementRepository interface {
	Metrics(ctx context.Context) (ManagementMetrics, error)
	ListBucketSummaries(ctx context.Context) ([]BucketSummary, error)
	GetBucketSummary(ctx context.Context, bucketName string) (BucketSummary, error)
}

type sqliteManagementRepository struct {
	db *sql.DB
}

func NewManagementRepository(db *sql.DB) ManagementRepository {
	return &sqliteManagementRepository{db: db}
}

func (r *sqliteManagementRepository) Metrics(ctx context.Context) (ManagementMetrics, error) {
	const q = `
SELECT
  bucket_stats.bucket_count,
  object_stats.object_count,
  object_stats.total_object_bytes,
  user_stats.user_count,
  user_stats.active_user_count
FROM (SELECT count(*) AS bucket_count FROM buckets) bucket_stats
CROSS JOIN (
  SELECT count(*) AS object_count, COALESCE(sum(size), 0) AS total_object_bytes
  FROM objects
) object_stats
CROSS JOIN (
  SELECT count(*) AS user_count, COALESCE(sum(CASE WHEN is_active != 0 THEN 1 ELSE 0 END), 0) AS active_user_count
  FROM users
) user_stats`

	var metrics ManagementMetrics
	if err := r.db.QueryRowContext(ctx, q).Scan(
		&metrics.BucketCount,
		&metrics.ObjectCount,
		&metrics.TotalObjectBytes,
		&metrics.UserCount,
		&metrics.ActiveUserCount,
	); err != nil {
		return ManagementMetrics{}, fmt.Errorf("management metrics: %w", err)
	}

	return metrics, nil
}

func (r *sqliteManagementRepository) ListBucketSummaries(ctx context.Context) ([]BucketSummary, error) {
	const q = `
SELECT b.name, b.owner_id, b.created_at, count(o.id), COALESCE(sum(o.size), 0)
FROM buckets b
LEFT JOIN objects o ON o.bucket_name = b.name
GROUP BY b.name, b.owner_id, b.created_at
ORDER BY b.created_at ASC`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list bucket summaries: %w", err)
	}
	defer rows.Close()

	var summaries []BucketSummary
	for rows.Next() {
		var summary BucketSummary
		var createdAt string
		if err := rows.Scan(
			&summary.Name,
			&summary.OwnerID,
			&createdAt,
			&summary.ObjectCount,
			&summary.TotalObjectBytes,
		); err != nil {
			return nil, fmt.Errorf("list bucket summaries scan: %w", err)
		}
		summary.CreatedAt, err = parseTimestamp(createdAt)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list bucket summaries rows: %w", err)
	}

	return summaries, nil
}

func (r *sqliteManagementRepository) GetBucketSummary(ctx context.Context, bucketName string) (BucketSummary, error) {
	const q = `
SELECT b.name, b.owner_id, b.created_at, count(o.id), COALESCE(sum(o.size), 0)
FROM buckets b
LEFT JOIN objects o ON o.bucket_name = b.name
WHERE b.name = ?
GROUP BY b.name, b.owner_id, b.created_at`

	var summary BucketSummary
	var createdAt string
	err := r.db.QueryRowContext(ctx, q, bucketName).Scan(
		&summary.Name,
		&summary.OwnerID,
		&createdAt,
		&summary.ObjectCount,
		&summary.TotalObjectBytes,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return BucketSummary{}, ErrBucketNotFound
	}
	if err != nil {
		return BucketSummary{}, fmt.Errorf("get bucket summary: %w", err)
	}

	summary.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return BucketSummary{}, err
	}

	return summary, nil
}
