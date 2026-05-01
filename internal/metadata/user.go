package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// User represents a row in the users table.
type User struct {
	ID             string
	DisplayName    string
	AccessKeyID    string
	SecretHash     string
	SigV4AccessKeyID string
	SigV4SecretKey   string
	Role           string
	IsActive       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// UserRepository defines CRUD operations for users.
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id string) (*User, error)
	GetByAccessKeyID(ctx context.Context, accessKeyID string) (*User, error)
	List(ctx context.Context) ([]User, error)
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id string) error
}

// SigV4UserRepository is a narrow interface used only by the SigV4
// authenticator.  It is kept separate from UserRepository so that the
// raw secret is not exposed on the general public API.
type SigV4UserRepository interface {
	GetBySigV4AccessKeyID(ctx context.Context, accessKeyID string) (*User, error)
}

// ErrUserNotFound is returned when a user lookup yields no rows.
var ErrUserNotFound = errors.New("user not found")

type sqliteUserRepository struct {
	db *sql.DB
}

// NewUserRepository returns a UserRepository backed by the given *sql.DB.
func NewUserRepository(db *sql.DB) UserRepository {
	return &sqliteUserRepository{db: db}
}

// NewSigV4UserRepository returns a SigV4UserRepository backed by the given *sql.DB.
func NewSigV4UserRepository(db *sql.DB) SigV4UserRepository {
	return &sqliteUserRepository{db: db}
}

func (r *sqliteUserRepository) Create(ctx context.Context, user *User) error {
	const q = `
		INSERT INTO users (id, display_name, access_key_id, secret_hash, sigv4_access_key_id, sigv4_secret_key, role, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// Preserve NULL in the database when SigV4 credentials are not provided.
	// SQLite treats empty strings as real values, so writing '' would cause
	// duplicate-key errors on the unique index when creating multiple users
	// without SigV4 credentials.
	sigv4Key := sql.NullString{String: user.SigV4AccessKeyID, Valid: user.SigV4AccessKeyID != ""}
	sigv4Secret := sql.NullString{String: user.SigV4SecretKey, Valid: user.SigV4SecretKey != ""}

	isActive := boolToInt(user.IsActive)
	_, err := r.db.ExecContext(ctx, q,
		user.ID,
		user.DisplayName,
		user.AccessKeyID,
		user.SecretHash,
		sigv4Key,
		sigv4Secret,
		user.Role,
		isActive,
		user.CreatedAt.UTC(),
		user.UpdatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}

	return nil
}

func (r *sqliteUserRepository) GetByID(ctx context.Context, id string) (*User, error) {
	const q = `
		SELECT id, display_name, access_key_id, secret_hash, sigv4_access_key_id, sigv4_secret_key, role, is_active, created_at, updated_at
		FROM users
		WHERE id = ?`

	row := r.db.QueryRowContext(ctx, q, id)
	u, err := scanUser(row)
	if err != nil {
		return nil, err
	}
	u.SigV4SecretKey = "" // never expose secret on ordinary reads
	return u, nil
}

func (r *sqliteUserRepository) GetByAccessKeyID(ctx context.Context, accessKeyID string) (*User, error) {
	const q = `
		SELECT id, display_name, access_key_id, secret_hash, sigv4_access_key_id, sigv4_secret_key, role, is_active, created_at, updated_at
		FROM users
		WHERE access_key_id = ?`

	row := r.db.QueryRowContext(ctx, q, accessKeyID)
	u, err := scanUser(row)
	if err != nil {
		return nil, err
	}
	u.SigV4SecretKey = "" // never expose secret on ordinary reads
	return u, nil
}

func (r *sqliteUserRepository) GetBySigV4AccessKeyID(ctx context.Context, accessKeyID string) (*User, error) {
	const q = `
		SELECT id, display_name, access_key_id, secret_hash, sigv4_access_key_id, sigv4_secret_key, role, is_active, created_at, updated_at
		FROM users
		WHERE sigv4_access_key_id = ?`

	row := r.db.QueryRowContext(ctx, q, accessKeyID)
	return scanUser(row) // keep secret – only caller is the authenticator
}

func (r *sqliteUserRepository) List(ctx context.Context) ([]User, error) {
	const q = `
		SELECT id, display_name, access_key_id, secret_hash, sigv4_access_key_id, sigv4_secret_key, role, is_active, created_at, updated_at
		FROM users
		ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, fmt.Errorf("list users scan: %w", err)
		}
		u.SigV4SecretKey = "" // never expose secret on ordinary reads
		users = append(users, *u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users rows: %w", err)
	}

	return users, nil
}

func (r *sqliteUserRepository) Update(ctx context.Context, user *User) error {
	// SigV4 credentials are managed separately (created at user creation,
	// revoked via deletion / re-creation).  They are intentionally omitted
	// from this general-purpose update so that ordinary read-modify-update
	// cycles cannot accidentally wipe or alter them.
	const q = `
		UPDATE users
		SET display_name  = ?,
		    access_key_id = ?,
		    secret_hash   = ?,
		    role          = ?,
		    is_active     = ?,
		    updated_at    = ?
		WHERE id = ?`

	result, err := r.db.ExecContext(ctx, q,
		user.DisplayName,
		user.AccessKeyID,
		user.SecretHash,
		user.Role,
		boolToInt(user.IsActive),
		time.Now().UTC(),
		user.ID,
	)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update user rows affected: %w", err)
	}
	if rows == 0 {
		return ErrUserNotFound
	}

	return nil
}

func (r *sqliteUserRepository) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM users WHERE id = ?`

	result, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete user rows affected: %w", err)
	}
	if rows == 0 {
		return ErrUserNotFound
	}

	return nil
}

// scanUser scans a single *sql.Row into a User.
func scanUser(row *sql.Row) (*User, error) {
	var u User
	var isActive int
	var createdAt, updatedAt string
	var sigv4Key, sigv4Secret sql.NullString

	err := row.Scan(
		&u.ID,
		&u.DisplayName,
		&u.AccessKeyID,
		&u.SecretHash,
		&sigv4Key,
		&sigv4Secret,
		&u.Role,
		&isActive,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}

	u.SigV4AccessKeyID = sigv4Key.String
	u.SigV4SecretKey = sigv4Secret.String
	u.IsActive = isActive != 0
	u.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, err
	}
	u.UpdatedAt, err = parseTimestamp(updatedAt)
	if err != nil {
		return nil, err
	}

	return &u, nil
}

// scanUserRow scans a *sql.Rows (multi-row query) into a User.
func scanUserRow(rows *sql.Rows) (*User, error) {
	var u User
	var isActive int
	var createdAt, updatedAt string
	var sigv4Key, sigv4Secret sql.NullString

	err := rows.Scan(
		&u.ID,
		&u.DisplayName,
		&u.AccessKeyID,
		&u.SecretHash,
		&sigv4Key,
		&sigv4Secret,
		&u.Role,
		&isActive,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan user row: %w", err)
	}

	u.SigV4AccessKeyID = sigv4Key.String
	u.SigV4SecretKey = sigv4Secret.String
	u.IsActive = isActive != 0
	u.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, err
	}
	u.UpdatedAt, err = parseTimestamp(updatedAt)
	if err != nil {
		return nil, err
	}

	return &u, nil
}

// boolToInt converts a bool to SQLite's INTEGER representation.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// parseTimestamp parses SQLite timestamp strings into time.Time.
func parseTimestamp(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		time.RFC3339,
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse timestamp %q: unrecognised format", s)
}
