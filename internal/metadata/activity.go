package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type ObjectActivity struct {
	ID          string
	Action      string
	BucketName  string
	ObjectKey   string
	Size        int64
	ETag        string
	ActorUserID string
	CreatedAt   time.Time
}

type ActivityListFilter struct {
	BucketName string
	Action     string
	Limit      int
}

type ActivityRepository interface {
	Create(ctx context.Context, activity *ObjectActivity) error
	List(ctx context.Context, filter ActivityListFilter) ([]ObjectActivity, error)
}

type sqliteActivityRepository struct {
	db *sql.DB
}

func NewActivityRepository(db *sql.DB) ActivityRepository {
	return &sqliteActivityRepository{db: db}
}

func (r *sqliteActivityRepository) Create(ctx context.Context, activity *ObjectActivity) error {
	const q = `
INSERT INTO object_activity (id, action, bucket_name, object_key, size, etag, actor_user_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	objectKey := sql.NullString{String: activity.ObjectKey, Valid: activity.ObjectKey != ""}
	size := sql.NullInt64{Int64: activity.Size, Valid: activity.Size != 0}
	etag := sql.NullString{String: activity.ETag, Valid: activity.ETag != ""}
	actorUserID := sql.NullString{String: activity.ActorUserID, Valid: activity.ActorUserID != ""}
	createdAt := activity.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	if _, err := r.db.ExecContext(ctx, q,
		activity.ID,
		activity.Action,
		activity.BucketName,
		objectKey,
		size,
		etag,
		actorUserID,
		createdAt.UTC(),
	); err != nil {
		return fmt.Errorf("create object activity: %w", err)
	}

	return nil
}

func (r *sqliteActivityRepository) List(ctx context.Context, filter ActivityListFilter) ([]ObjectActivity, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}

	q := `
SELECT id, action, bucket_name, object_key, size, etag, actor_user_id, created_at
FROM object_activity
WHERE (? = '' OR bucket_name = ?)
  AND (? = '' OR action = ?)
ORDER BY created_at DESC, id DESC
LIMIT ?`

	rows, err := r.db.QueryContext(ctx, q,
		filter.BucketName, filter.BucketName,
		filter.Action, filter.Action,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list object activity: %w", err)
	}
	defer rows.Close()

	var activities []ObjectActivity
	for rows.Next() {
		activity, err := scanActivityRow(rows)
		if err != nil {
			return nil, err
		}
		activities = append(activities, activity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list object activity rows: %w", err)
	}

	return activities, nil
}

func scanActivityRow(rows *sql.Rows) (ObjectActivity, error) {
	var activity ObjectActivity
	var objectKey, etag, actorUserID sql.NullString
	var size sql.NullInt64
	var createdAt string

	if err := rows.Scan(
		&activity.ID,
		&activity.Action,
		&activity.BucketName,
		&objectKey,
		&size,
		&etag,
		&actorUserID,
		&createdAt,
	); err != nil {
		return ObjectActivity{}, fmt.Errorf("scan object activity row: %w", err)
	}

	activity.ObjectKey = objectKey.String
	if size.Valid {
		activity.Size = size.Int64
	}
	activity.ETag = etag.String
	activity.ActorUserID = actorUserID.String

	var err error
	activity.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return ObjectActivity{}, err
	}

	return activity, nil
}
