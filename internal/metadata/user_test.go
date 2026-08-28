package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"

	_ "modernc.org/sqlite"
)

// createUsersTable is the DDL used for test isolation.
// We only create the users table here — other tables are tested in their own files.
const createUsersTable = `
CREATE TABLE IF NOT EXISTS users (
    id                  TEXT PRIMARY KEY,
    display_name        TEXT NOT NULL,
    access_key_id       TEXT NOT NULL UNIQUE,
    secret_hash         TEXT NOT NULL,
    sigv4_access_key_id TEXT UNIQUE,
    sigv4_secret_key    TEXT,
    role                TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    is_active           INTEGER NOT NULL DEFAULT 1,
    created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

// openTestDB opens an in-memory SQLite database and creates the users table.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(createUsersTable); err != nil {
		t.Fatalf("create users table: %v", err)
	}

	return db
}

// newTestUser returns a User with sensible defaults for testing.
func newTestUser() *User {
	now := time.Now().UTC().Truncate(time.Second)
	return &User{
		ID:               uuid.New().String(),
		DisplayName:      "Alice",
		AccessKeyID:      "AKIAIOSFODNN7EXAMPLE",
		SecretHash:       "sha256hashvalue",
		SigV4AccessKeyID: "fbsv4_testuser_001",
		SigV4SecretKey:   "sigv4secretvalue",
		Role:             "member",
		IsActive:         true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func TestUserCreate(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	u := newTestUser()
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestUserCreate_DuplicateAccessKeyID(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	u := newTestUser()
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Same access_key_id, different id — must violate UNIQUE constraint.
	duplicate := *u
	duplicate.ID = uuid.New().String()
	if err := repo.Create(ctx, &duplicate); err == nil {
		t.Fatal("expected error on duplicate access_key_id, got nil")
	}
}

func TestUserCreate_MultipleWithoutSigV4(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	for i := range 3 {
		u := &User{
			ID:               uuid.New().String(),
			DisplayName:      fmt.Sprintf("NoSigV4 %d", i),
			AccessKeyID:      fmt.Sprintf("key_no_sigv4_%d", i),
			SecretHash:       "hash",
			SigV4AccessKeyID: "",
			SigV4SecretKey:   "",
			Role:             "member",
			IsActive:         true,
			CreatedAt:        time.Now().UTC().Truncate(time.Second),
			UpdatedAt:        time.Now().UTC().Truncate(time.Second),
		}
		if err := repo.Create(ctx, u); err != nil {
			t.Fatalf("Create user %d: %v", i, err)
		}
	}

	users, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 3 {
		t.Errorf("expected 3 users, got %d", len(users))
	}
}

func TestUserGetByID(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	want := newTestUser()
	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	assertUsersEqual(t, want, got)
}

func TestUserGetByID_NotFound(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	_, err := repo.GetByID(ctx, "nonexistent-id")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestUserRead_LegacyUserWithNullSigV4(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	// Simulate a pre-F5 user with NULL sigv4 columns
	u := &User{
		ID:               uuid.New().String(),
		DisplayName:      "Legacy User",
		AccessKeyID:      "AKIA_LEGACY",
		SecretHash:       "legacyhash",
		SigV4AccessKeyID: "",
		SigV4SecretKey:   "",
		Role:             "member",
		IsActive:         true,
		CreatedAt:        time.Now().UTC().Truncate(time.Second),
		UpdatedAt:        time.Now().UTC().Truncate(time.Second),
	}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify GetByID can read the user back
	got, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.SigV4AccessKeyID != "" {
		t.Errorf("SigV4AccessKeyID = %q, want empty string", got.SigV4AccessKeyID)
	}
	if got.SigV4SecretKey != "" {
		t.Errorf("SigV4SecretKey = %q, want empty string", got.SigV4SecretKey)
	}

	// Verify GetByAccessKeyID can read the user back
	got, err = repo.GetByAccessKeyID(ctx, u.AccessKeyID)
	if err != nil {
		t.Fatalf("GetByAccessKeyID: %v", err)
	}
	if got.SigV4AccessKeyID != "" {
		t.Errorf("GetByAccessKeyID SigV4AccessKeyID = %q, want empty string", got.SigV4AccessKeyID)
	}

	// Verify List can read the user back
	users, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].SigV4AccessKeyID != "" {
		t.Errorf("List SigV4AccessKeyID = %q, want empty string", users[0].SigV4AccessKeyID)
	}
}

func TestUserGetByAccessKeyID(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	want := newTestUser()
	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByAccessKeyID(ctx, want.AccessKeyID)
	if err != nil {
		t.Fatalf("GetByAccessKeyID: %v", err)
	}

	assertUsersEqual(t, want, got)
}

func TestUserGetByAccessKeyID_NotFound(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	_, err := repo.GetByAccessKeyID(ctx, "NO_SUCH_KEY")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestUserGetBySigV4AccessKeyID(t *testing.T) {
	db := openTestDB(t)
	repo := NewUserRepository(db)
	sigv4Repo := NewSigV4UserRepository(db)
	ctx := context.Background()

	want := newTestUser()
	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := sigv4Repo.GetBySigV4AccessKeyID(ctx, want.SigV4AccessKeyID)
	if err != nil {
		t.Fatalf("GetBySigV4AccessKeyID: %v", err)
	}

	assertUsersEqual(t, want, got)
	// GetBySigV4AccessKeyID is the only read path that returns the secret.
	if got.SigV4SecretKey != want.SigV4SecretKey {
		t.Errorf("SigV4SecretKey: want %q, got %q", want.SigV4SecretKey, got.SigV4SecretKey)
	}
}

func TestUserGetBySigV4AccessKeyID_NotFound(t *testing.T) {
	repo := NewSigV4UserRepository(openTestDB(t))
	ctx := context.Background()

	_, err := repo.GetBySigV4AccessKeyID(ctx, "NO_SUCH_KEY")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestUserList(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	// Empty list should return no error and zero items.
	users, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List (empty): %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("expected 0 users, got %d", len(users))
	}

	// Insert two users.
	u1 := newTestUser()
	u2 := &User{
		ID:               uuid.New().String(),
		DisplayName:      "Bob",
		AccessKeyID:      "AKIAI2NDUSER",
		SecretHash:       "anotherhash",
		SigV4AccessKeyID: "fbsv4_testuser_002",
		SigV4SecretKey:   "anothersigv4secret",
		Role:             "admin",
		IsActive:         false,
		CreatedAt:        time.Now().UTC().Truncate(time.Second),
		UpdatedAt:        time.Now().UTC().Truncate(time.Second),
	}

	for _, u := range []*User{u1, u2} {
		if err := repo.Create(ctx, u); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	users, err = repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
}

func TestUserUpdate(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	u := newTestUser()
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	u.DisplayName = "Alice Updated"
	u.Role = "admin"
	u.IsActive = false

	if err := repo.Update(ctx, u); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID after Update: %v", err)
	}

	if got.DisplayName != "Alice Updated" {
		t.Errorf("DisplayName: want %q, got %q", "Alice Updated", got.DisplayName)
	}
	if got.Role != "admin" {
		t.Errorf("Role: want %q, got %q", "admin", got.Role)
	}
	if got.IsActive {
		t.Error("IsActive: want false, got true")
	}
}

func TestUserUpdate_NotFound(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	ghost := newTestUser()
	ghost.ID = "does-not-exist"

	if err := repo.Update(ctx, ghost); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestUserUpdate_LegacyUserPreservesNullSigV4(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	// Simulate two pre-F5 users by inserting rows without SigV4 columns.
	for i := range 2 {
		id := uuid.New().String()
		_, err := repo.(*sqliteUserRepository).db.ExecContext(ctx,
			`INSERT INTO users (id, display_name, access_key_id, secret_hash, role) VALUES (?, ?, ?, ?, ?)`,
			id,
			fmt.Sprintf("Legacy %d", i),
			fmt.Sprintf("legacy_key_%d", i),
			"hash",
			"member",
		)
		if err != nil {
			t.Fatalf("insert legacy user %d: %v", i, err)
		}

		// Read back via repository (NULL -> empty string in Go)
		u, err := repo.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID legacy user %d: %v", i, err)
		}
		if u.SigV4AccessKeyID != "" || u.SigV4SecretKey != "" {
			t.Fatalf("legacy user %d: expected empty sigv4 fields", i)
		}

		// Update the user — must not fail on unique index collision
		u.DisplayName = fmt.Sprintf("Updated %d", i)
		if err := repo.Update(ctx, u); err != nil {
			t.Fatalf("Update legacy user %d: %v", i, err)
		}

		// Verify NULL was preserved in the database
		var keyNull, secretNull bool
		err = repo.(*sqliteUserRepository).db.QueryRowContext(ctx,
			`SELECT sigv4_access_key_id IS NULL, sigv4_secret_key IS NULL FROM users WHERE id = ?`, id,
		).Scan(&keyNull, &secretNull)
		if err != nil {
			t.Fatalf("check nulls for legacy user %d: %v", i, err)
		}
		if !keyNull {
			t.Errorf("legacy user %d: sigv4_access_key_id should be NULL", i)
		}
		if !secretNull {
			t.Errorf("legacy user %d: sigv4_secret_key should be NULL", i)
		}
	}
}

func TestUserDelete(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	u := newTestUser()
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Should no longer exist.
	if _, err := repo.GetByID(ctx, u.ID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound after Delete, got %v", err)
	}
}

func TestUserDelete_NotFound(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	if err := repo.Delete(ctx, "no-such-id"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestUserCRUD_RoundTrip(t *testing.T) {
	repo := NewUserRepository(openTestDB(t))
	ctx := context.Background()

	// Create
	want := newTestUser()
	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Read
	got, err := repo.GetByID(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	assertUsersEqual(t, want, got)

	// Update
	want.DisplayName = "Alice v2"
	want.Role = "admin"
	if err := repo.Update(ctx, want); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err = repo.GetByID(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.DisplayName != "Alice v2" {
		t.Errorf("DisplayName mismatch after update: %q", got.DisplayName)
	}

	// Delete
	if err := repo.Delete(ctx, want.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := repo.GetByID(ctx, want.ID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound after delete, got %v", err)
	}
}

// assertUsersEqual compares two Users for field equality.
func assertUsersEqual(t *testing.T, want, got *User) {
	t.Helper()

	if got.ID != want.ID {
		t.Errorf("ID: want %q, got %q", want.ID, got.ID)
	}
	if got.DisplayName != want.DisplayName {
		t.Errorf("DisplayName: want %q, got %q", want.DisplayName, got.DisplayName)
	}
	if got.AccessKeyID != want.AccessKeyID {
		t.Errorf("AccessKeyID: want %q, got %q", want.AccessKeyID, got.AccessKeyID)
	}
	if got.SecretHash != want.SecretHash {
		t.Errorf("SecretHash: want %q, got %q", want.SecretHash, got.SecretHash)
	}
	if got.SigV4AccessKeyID != want.SigV4AccessKeyID {
		t.Errorf("SigV4AccessKeyID: want %q, got %q", want.SigV4AccessKeyID, got.SigV4AccessKeyID)
	}
	if got.Role != want.Role {
		t.Errorf("Role: want %q, got %q", want.Role, got.Role)
	}
	if got.IsActive != want.IsActive {
		t.Errorf("IsActive: want %v, got %v", want.IsActive, got.IsActive)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt: want %v, got %v", want.CreatedAt, got.CreatedAt)
	}
}
