package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// grantableActions is the set of actions that may be stored on grant rows.
// Kept local to avoid an import cycle with internal/authz (auth → metadata → authz → auth).
var grantableActions = map[string]struct{}{
	"s3:ListBucket":               {},
	"s3:GetObject":                {},
	"s3:PutObject":                {},
	"s3:DeleteObject":             {},
	"s3:ListMultipartUploadParts": {},
	"s3:AbortMultipartUpload":     {},
}

func isGrantableAction(action string) bool {
	_, ok := grantableActions[action]
	return ok
}

// Grant represents a row in the grants table.
type Grant struct {
	ID            string
	BucketName    string
	GranteeUserID string
	Action        string
	KeyPrefix     string
	IsActive      bool
	CreatedBy     string
	Note          string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ErrGrantNotFound is returned when a grant lookup yields no rows.
var ErrGrantNotFound = errors.New("grant not found")

// ErrInvalidGrantAction is returned when a non-grantable action is written.
var ErrInvalidGrantAction = errors.New("invalid grant action")

// GrantRepository persists and queries resource grants.
type GrantRepository interface {
	Create(ctx context.Context, grant *Grant) error
	// CreateIdempotent inserts a grant or returns the existing active grant
	// with the same (bucket, grantee, action, prefix).
	CreateIdempotent(ctx context.Context, grant *Grant) (*Grant, bool, error)
	GetByID(ctx context.Context, id string) (*Grant, error)
	Update(ctx context.Context, grant *Grant) error
	Delete(ctx context.Context, id string) error
	ListByBucket(ctx context.Context, bucketName string) ([]Grant, error)
	ListByGrantee(ctx context.Context, granteeUserID string) ([]Grant, error)
	ListActiveForGranteeBucket(ctx context.Context, granteeUserID, bucketName string) ([]Grant, error)
	ListBucketNamesWithActiveGrants(ctx context.Context, granteeUserID string) ([]string, error)
}

type sqliteGrantRepository struct {
	db *sql.DB
}

// NewGrantRepository returns a GrantRepository backed by the given *sql.DB.
func NewGrantRepository(db *sql.DB) GrantRepository {
	return &sqliteGrantRepository{db: db}
}

func (r *sqliteGrantRepository) Create(ctx context.Context, grant *Grant) error {
	if err := validateGrantWrite(grant); err != nil {
		return err
	}

	const q = `
		INSERT INTO grants (
			id, bucket_name, grantee_user_id, action, key_prefix,
			is_active, created_by, note, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	createdBy := sql.NullString{String: grant.CreatedBy, Valid: grant.CreatedBy != ""}
	note := sql.NullString{String: grant.Note, Valid: grant.Note != ""}

	_, err := r.db.ExecContext(ctx, q,
		grant.ID,
		grant.BucketName,
		grant.GranteeUserID,
		grant.Action,
		grant.KeyPrefix,
		boolToInt(grant.IsActive),
		createdBy,
		note,
		grant.CreatedAt.UTC(),
		grant.UpdatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("create grant: %w", err)
	}
	return nil
}

func (r *sqliteGrantRepository) CreateIdempotent(ctx context.Context, grant *Grant) (*Grant, bool, error) {
	if err := validateGrantWrite(grant); err != nil {
		return nil, false, err
	}

	existing, err := r.findActiveDuplicate(ctx, grant.BucketName, grant.GranteeUserID, grant.Action, grant.KeyPrefix)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, true, nil
	}

	if err := r.Create(ctx, grant); err != nil {
		// Race: another writer may have inserted the same active grant.
		if isUniqueConstraintError(err) {
			existing, lookupErr := r.findActiveDuplicate(ctx, grant.BucketName, grant.GranteeUserID, grant.Action, grant.KeyPrefix)
			if lookupErr != nil {
				return nil, false, lookupErr
			}
			if existing != nil {
				return existing, true, nil
			}
		}
		return nil, false, err
	}
	return grant, false, nil
}

func (r *sqliteGrantRepository) findActiveDuplicate(ctx context.Context, bucketName, granteeUserID, action, keyPrefix string) (*Grant, error) {
	const q = `
		SELECT id, bucket_name, grantee_user_id, action, key_prefix,
		       is_active, created_by, note, created_at, updated_at
		FROM grants
		WHERE bucket_name = ?
		  AND grantee_user_id = ?
		  AND action = ?
		  AND key_prefix = ?
		  AND is_active = 1
		LIMIT 1`

	row := r.db.QueryRowContext(ctx, q, bucketName, granteeUserID, action, keyPrefix)
	grant, err := scanGrant(row)
	if errors.Is(err, ErrGrantNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return grant, nil
}

func (r *sqliteGrantRepository) GetByID(ctx context.Context, id string) (*Grant, error) {
	const q = `
		SELECT id, bucket_name, grantee_user_id, action, key_prefix,
		       is_active, created_by, note, created_at, updated_at
		FROM grants
		WHERE id = ?`

	row := r.db.QueryRowContext(ctx, q, id)
	return scanGrant(row)
}

func (r *sqliteGrantRepository) Update(ctx context.Context, grant *Grant) error {
	if grant == nil || strings.TrimSpace(grant.ID) == "" {
		return ErrGrantNotFound
	}
	if grant.IsActive && !isGrantableAction(grant.Action) {
		return ErrInvalidGrantAction
	}

	const q = `
		UPDATE grants
		SET key_prefix = ?,
		    is_active = ?,
		    note = ?,
		    updated_at = ?
		WHERE id = ?`

	note := sql.NullString{String: grant.Note, Valid: grant.Note != ""}
	result, err := r.db.ExecContext(ctx, q,
		grant.KeyPrefix,
		boolToInt(grant.IsActive),
		note,
		time.Now().UTC(),
		grant.ID,
	)
	if err != nil {
		return fmt.Errorf("update grant: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update grant rows affected: %w", err)
	}
	if rows == 0 {
		return ErrGrantNotFound
	}
	return nil
}

func (r *sqliteGrantRepository) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM grants WHERE id = ?`

	result, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete grant: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete grant rows affected: %w", err)
	}
	if rows == 0 {
		return ErrGrantNotFound
	}
	return nil
}

func (r *sqliteGrantRepository) ListByBucket(ctx context.Context, bucketName string) ([]Grant, error) {
	const q = `
		SELECT id, bucket_name, grantee_user_id, action, key_prefix,
		       is_active, created_by, note, created_at, updated_at
		FROM grants
		WHERE bucket_name = ?
		ORDER BY created_at ASC, id ASC`

	return r.list(ctx, q, bucketName)
}

func (r *sqliteGrantRepository) ListByGrantee(ctx context.Context, granteeUserID string) ([]Grant, error) {
	const q = `
		SELECT id, bucket_name, grantee_user_id, action, key_prefix,
		       is_active, created_by, note, created_at, updated_at
		FROM grants
		WHERE grantee_user_id = ?
		ORDER BY created_at ASC, id ASC`

	return r.list(ctx, q, granteeUserID)
}

func (r *sqliteGrantRepository) ListActiveForGranteeBucket(ctx context.Context, granteeUserID, bucketName string) ([]Grant, error) {
	const q = `
		SELECT id, bucket_name, grantee_user_id, action, key_prefix,
		       is_active, created_by, note, created_at, updated_at
		FROM grants
		WHERE grantee_user_id = ?
		  AND bucket_name = ?
		  AND is_active = 1`

	return r.list(ctx, q, granteeUserID, bucketName)
}

func (r *sqliteGrantRepository) ListBucketNamesWithActiveGrants(ctx context.Context, granteeUserID string) ([]string, error) {
	const q = `
		SELECT DISTINCT bucket_name
		FROM grants
		WHERE grantee_user_id = ?
		  AND is_active = 1
		ORDER BY bucket_name ASC`

	rows, err := r.db.QueryContext(ctx, q, granteeUserID)
	if err != nil {
		return nil, fmt.Errorf("list grant bucket names: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan grant bucket name: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list grant bucket names rows: %w", err)
	}
	return names, nil
}

func (r *sqliteGrantRepository) list(ctx context.Context, query string, args ...any) ([]Grant, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer rows.Close()

	var grants []Grant
	for rows.Next() {
		g, err := scanGrantRow(rows)
		if err != nil {
			return nil, err
		}
		grants = append(grants, *g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list grants rows: %w", err)
	}
	return grants, nil
}

func validateGrantWrite(grant *Grant) error {
	if grant == nil {
		return fmt.Errorf("grant is nil")
	}
	if !isGrantableAction(grant.Action) {
		return ErrInvalidGrantAction
	}
	return nil
}

func scanGrant(row *sql.Row) (*Grant, error) {
	var g Grant
	var isActive int
	var createdBy, note sql.NullString
	var createdAt, updatedAt string

	err := row.Scan(
		&g.ID,
		&g.BucketName,
		&g.GranteeUserID,
		&g.Action,
		&g.KeyPrefix,
		&isActive,
		&createdBy,
		&note,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrGrantNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan grant: %w", err)
	}

	g.IsActive = isActive != 0
	g.CreatedBy = createdBy.String
	g.Note = note.String

	var parseErr error
	g.CreatedAt, parseErr = parseTimestamp(createdAt)
	if parseErr != nil {
		return nil, parseErr
	}
	g.UpdatedAt, parseErr = parseTimestamp(updatedAt)
	if parseErr != nil {
		return nil, parseErr
	}
	return &g, nil
}

func scanGrantRow(rows *sql.Rows) (*Grant, error) {
	var g Grant
	var isActive int
	var createdBy, note sql.NullString
	var createdAt, updatedAt string

	err := rows.Scan(
		&g.ID,
		&g.BucketName,
		&g.GranteeUserID,
		&g.Action,
		&g.KeyPrefix,
		&isActive,
		&createdBy,
		&note,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan grant row: %w", err)
	}

	g.IsActive = isActive != 0
	g.CreatedBy = createdBy.String
	g.Note = note.String

	g.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, err
	}
	g.UpdatedAt, err = parseTimestamp(updatedAt)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "constraint failed")
}
