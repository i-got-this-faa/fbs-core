package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/i-got-this-faa/fbs/internal/iam"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Grant represents a row in the grants table.
type Grant struct {
	ID            string
	BucketName    string
	GranteeUserID string
	Action        iam.Action
	KeyPrefix     string
	IsActive      bool
	CreatedBy     string
	Note          string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// GrantCreateResult is the outcome for one grant in a batch create.
type GrantCreateResult struct {
	Grant   Grant
	Existed bool
}

// ErrGrantNotFound is returned when a grant lookup yields no rows.
var ErrGrantNotFound = errors.New("grant not found")

// ErrInvalidGrantAction is returned when a non-grantable action is written.
var ErrInvalidGrantAction = errors.New("invalid grant action")

// ErrDuplicateGrant is returned when an update would create a second active
// grant for the same (bucket, grantee, action, prefix).
var ErrDuplicateGrant = errors.New("duplicate active grant")

// GrantRepository persists and queries resource grants.
type GrantRepository interface {
	Create(ctx context.Context, grant *Grant) error
	// CreateIdempotentBatch stores every grant in one transaction. A grant that
	// matches an existing active grant (bucket, grantee, action, prefix) is
	// returned as Existed instead of inserted.
	CreateIdempotentBatch(ctx context.Context, grants []Grant) ([]GrantCreateResult, error)
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

	if _, err := r.db.ExecContext(ctx, q, grantInsertArgs(grant)...); err != nil {
		return fmt.Errorf("create grant: %w", err)
	}
	return nil
}

func grantInsertArgs(grant *Grant) []any {
	return []any{
		grant.ID,
		grant.BucketName,
		grant.GranteeUserID,
		grant.Action,
		grant.KeyPrefix,
		boolToInt(grant.IsActive),
		sql.NullString{String: grant.CreatedBy, Valid: grant.CreatedBy != ""},
		sql.NullString{String: grant.Note, Valid: grant.Note != ""},
		grant.CreatedAt.UTC(),
		grant.UpdatedAt.UTC(),
	}
}

// insertGrantUnlessActiveDuplicate targets the partial unique index
// idx_grants_unique_active, so it skips only an active duplicate.
const insertGrantUnlessActiveDuplicate = `
	INSERT INTO grants (
		id, bucket_name, grantee_user_id, action, key_prefix,
		is_active, created_by, note, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (bucket_name, grantee_user_id, action, key_prefix) WHERE is_active = 1 DO NOTHING`

func (r *sqliteGrantRepository) CreateIdempotentBatch(ctx context.Context, grants []Grant) ([]GrantCreateResult, error) {
	for i := range grants {
		if err := validateGrantWrite(&grants[i]); err != nil {
			return nil, err
		}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin grant batch: %w", err)
	}
	defer tx.Rollback()

	results := make([]GrantCreateResult, 0, len(grants))
	for _, grant := range grants {
		result, err := tx.ExecContext(ctx, insertGrantUnlessActiveDuplicate, grantInsertArgs(&grant)...)
		if err != nil {
			return nil, fmt.Errorf("create grant: %w", err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("create grant rows affected: %w", err)
		}
		if inserted == 1 {
			results = append(results, GrantCreateResult{Grant: grant})
			continue
		}

		existing, err := findActiveDuplicate(ctx, tx, grant.BucketName, grant.GranteeUserID, grant.Action, grant.KeyPrefix)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, fmt.Errorf("create grant: insert skipped but no active duplicate found")
		}
		results = append(results, GrantCreateResult{Grant: *existing, Existed: true})
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit grant batch: %w", err)
	}
	return results, nil
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func findActiveDuplicate(ctx context.Context, db rowQuerier, bucketName, granteeUserID string, action iam.Action, keyPrefix string) (*Grant, error) {
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

	row := db.QueryRowContext(ctx, q, bucketName, granteeUserID, action, keyPrefix)
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
	if grant.IsActive && !grant.Action.IsGrantable() {
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
		if isUniqueConstraintError(err) {
			return ErrDuplicateGrant
		}
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
	if !grant.Action.IsGrantable() {
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
	if se, ok := errors.AsType[*sqlite.Error](err); ok {
		code := se.Code()
		if code == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return true
		}
		// Some paths only set the primary SQLITE_CONSTRAINT code.
		if code&0xff == sqlite3.SQLITE_CONSTRAINT &&
			strings.Contains(strings.ToLower(se.Error()), "unique") {
			return true
		}
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
