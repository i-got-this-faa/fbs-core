package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRun_InitialMigration(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify schema_migrations was recorded
	var version int
	err := db.QueryRow(`SELECT version FROM schema_migrations WHERE version = 1`).Scan(&version)
	if err != nil {
		t.Fatalf("schema_migrations not recorded: %v", err)
	}
	if version != 1 {
		t.Errorf("version = %d, want 1", version)
	}

	// Verify users table exists
	var count int
	err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&count)
	if err != nil {
		t.Fatalf("check users table: %v", err)
	}
	if count != 1 {
		t.Errorf("users table count = %d, want 1", count)
	}
}

func TestRun_Idempotent(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := Run(db); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	var count int
	err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&count)
	if err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 8 {
		t.Errorf("migration count = %d, want 8", count)
	}
}

func TestRunV2_ExistingUsers(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	// Apply v1 only
	if err := runMigrations(db, migrations[:1]); err != nil {
		t.Fatalf("run v1 migration: %v", err)
	}

	// Insert multiple pre-F5 users
	for i := 0; i < 3; i++ {
		_, err := db.Exec(
			`INSERT INTO users (id, display_name, access_key_id, secret_hash, role) VALUES (?, ?, ?, ?, ?)`,
			fmt.Sprintf("user-%d", i),
			fmt.Sprintf("User %d", i),
			fmt.Sprintf("key_%d", i),
			"hash",
			"member",
		)
		if err != nil {
			t.Fatalf("insert user %d: %v", i, err)
		}
	}

	// Apply v2 — must not fail on unique index collision
	if err := runMigrations(db, migrations); err != nil {
		t.Fatalf("run v2 migration: %v", err)
	}

	// Verify all users still exist and have NULL sigv4 columns
	rows, err := db.Query(`SELECT id, sigv4_access_key_id, sigv4_secret_key FROM users ORDER BY id`)
	if err != nil {
		t.Fatalf("query users: %v", err)
	}
	defer rows.Close()

	var found int
	for rows.Next() {
		var id string
		var sigv4Key, sigv4Secret sql.NullString
		if err := rows.Scan(&id, &sigv4Key, &sigv4Secret); err != nil {
			t.Fatalf("scan user: %v", err)
		}
		if sigv4Key.Valid {
			t.Errorf("user %s: expected sigv4_access_key_id to be NULL, got %q", id, sigv4Key.String)
		}
		if sigv4Secret.Valid {
			t.Errorf("user %s: expected sigv4_secret_key to be NULL, got %q", id, sigv4Secret.String)
		}
		found++
	}
	if found != 3 {
		t.Errorf("found %d users, want 3", found)
	}
}

func TestRun_BootstrapFromInitSQL(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	// Simulate a database bootstrapped from the v1 schema without schema_migrations tracking.
	// Use migrations[0].sql directly so this test stays aligned with the actual migration.
	if _, err := db.Exec(migrations[0].sql); err != nil {
		t.Fatalf("bootstrap from init sql: %v", err)
	}

	// Verify schema_migrations does not exist yet
	var count int
	err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&count)
	if err != nil {
		t.Fatalf("check schema_migrations: %v", err)
	}
	if count != 0 {
		t.Fatal("expected schema_migrations to not exist yet")
	}

	// Now run the migration chain — must succeed and apply v2
	if err := Run(db); err != nil {
		t.Fatalf("Run after manual bootstrap: %v", err)
	}

	// Verify all three migrations were recorded
	err = db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&count)
	if err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 8 {
		t.Errorf("migration count = %d, want 8", count)
	}

	// Verify sigv4 columns were added by v2
	var colCount int
	err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('users') WHERE name IN ('sigv4_access_key_id', 'sigv4_secret_key')`).Scan(&colCount)
	if err != nil {
		t.Fatalf("check sigv4 columns: %v", err)
	}
	if colCount != 2 {
		t.Errorf("sigv4 column count = %d, want 2", colCount)
	}

	// Verify content_type column was added by v3
	err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('multipart_uploads') WHERE name = 'content_type'`).Scan(&colCount)
	if err != nil {
		t.Fatalf("check content_type column: %v", err)
	}
	if colCount != 1 {
		t.Errorf("content_type column count = %d, want 1", colCount)
	}

	// Verify status column was added by v4
	err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('multipart_uploads') WHERE name = 'status'`).Scan(&colCount)
	if err != nil {
		t.Fatalf("check status column: %v", err)
	}
	if colCount != 1 {
		t.Errorf("status column count = %d, want 1", colCount)
	}

	// Verify status_updated_at column was added by v5
	err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('multipart_uploads') WHERE name = 'status_updated_at'`).Scan(&colCount)
	if err != nil {
		t.Fatalf("check status_updated_at column: %v", err)
	}
	if colCount != 1 {
		t.Errorf("status_updated_at column count = %d, want 1", colCount)
	}

	// Verify validation triggers were added by v6.
	err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name IN ('validate_multipart_upload_status_insert', 'validate_multipart_upload_status_update')`).Scan(&colCount)
	if err != nil {
		t.Fatalf("check multipart status triggers: %v", err)
	}
	if colCount != 2 {
		t.Errorf("multipart status trigger count = %d, want 2", colCount)
	}

	_, err = db.Exec(`
		INSERT INTO multipart_uploads (id, bucket_name, key, content_type, status, created_at, status_updated_at)
		VALUES ('bad-upload', 'missing-bucket', 'key', 'application/octet-stream', 'invalid', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err == nil {
		t.Fatal("expected invalid multipart status insert to fail")
	}
}

func TestRunMigration_TransactionalRollback(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	if err := ensureMigrationsTable(db); err != nil {
		t.Fatalf("ensure migrations table: %v", err)
	}

	// Create a migration with two statements: one that succeeds, one that fails
	badMigration := migration{
		version: 99,
		name:    "intentionally broken",
		sql: `
			CREATE TABLE IF NOT EXISTS test_tx_rollback (id INTEGER PRIMARY KEY);
			THIS_IS_INVALID_SQL;
		`,
	}

	err := runMigration(db, badMigration)
	if err == nil {
		t.Fatal("expected runMigration to fail, got nil")
	}

	// The first statement should have been rolled back
	var count int
	err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='test_tx_rollback'`).Scan(&count)
	if err != nil {
		t.Fatalf("check test table: %v", err)
	}
	if count != 0 {
		t.Errorf("test_tx_rollback exists (count=%d), expected it to be rolled back", count)
	}

	// The migration should not have been recorded
	err = db.QueryRow(`SELECT version FROM schema_migrations WHERE version = 99`).Scan(&count)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected migration 99 to not be recorded, got err=%v", err)
	}
}
