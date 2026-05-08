package metadata

import (
	"context"
	"database/sql"
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
  (SELECT count(*) FROM buckets) AS bucket_count,
  (SELECT count(*) FROM objects) AS object_count,
  (SELECT COALESCE(sum(size), 0) FROM objects) AS total_object_bytes,
  (SELECT count(*) FROM users) AS user_count,
  (SELECT count(*) FROM users WHERE is_active != 0) AS active_user_count`

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
