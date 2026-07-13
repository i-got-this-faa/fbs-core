package migrations

import (
	"database/sql"
	"fmt"
)

type migration struct {
	version int
	name    string
	sql     string
	run     func(*sql.Tx) error
}

var migrations = []migration{
	{
		version: 1,
		name:    "initial schema",
		sql: `
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    display_name  TEXT NOT NULL,
    access_key_id TEXT NOT NULL UNIQUE,
    secret_hash   TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    is_active     INTEGER NOT NULL DEFAULT 1,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS buckets (
    name       TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL REFERENCES users(id),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS objects (
    id             TEXT PRIMARY KEY,
    bucket_name    TEXT NOT NULL REFERENCES buckets(name),
    key            TEXT NOT NULL,
    size           INTEGER NOT NULL,
    etag           TEXT NOT NULL,
    content_type   TEXT NOT NULL DEFAULT 'application/octet-stream',
    storage_path   TEXT NOT NULL,
    created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(bucket_name, key)
);

CREATE INDEX IF NOT EXISTS idx_objects_bucket_prefix ON objects(bucket_name, key);

CREATE TABLE IF NOT EXISTS multipart_uploads (
    id           TEXT PRIMARY KEY,
    bucket_name  TEXT NOT NULL REFERENCES buckets(name),
    key          TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completing', 'aborted')),
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    status_updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS multipart_parts (
    upload_id   TEXT NOT NULL REFERENCES multipart_uploads(id) ON DELETE CASCADE,
    part_number INTEGER NOT NULL,
    size        INTEGER NOT NULL,
    etag        TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (upload_id, part_number)
);

CREATE TRIGGER IF NOT EXISTS validate_multipart_upload_status_insert
BEFORE INSERT ON multipart_uploads
FOR EACH ROW
WHEN NEW.status NOT IN ('active', 'completing', 'aborted')
BEGIN
    SELECT RAISE(ABORT, 'invalid multipart upload status');
END;

CREATE TRIGGER IF NOT EXISTS validate_multipart_upload_status_update
BEFORE UPDATE OF status ON multipart_uploads
FOR EACH ROW
WHEN NEW.status NOT IN ('active', 'completing', 'aborted')
BEGIN
    SELECT RAISE(ABORT, 'invalid multipart upload status');
END;
	`,
	},
	{
		version: 2,
		name:    "add sigv4 credentials",
		sql: `
ALTER TABLE users ADD COLUMN sigv4_access_key_id TEXT;
ALTER TABLE users ADD COLUMN sigv4_secret_key    TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_sigv4_access_key_id ON users(sigv4_access_key_id);
`,
	},
	{
		version: 3,
		name:    "add object activity",
		sql: `
CREATE TABLE object_activity (
    id            TEXT PRIMARY KEY,
    action        TEXT NOT NULL,
    bucket_name   TEXT NOT NULL,
    object_key    TEXT,
    size          INTEGER,
    etag          TEXT,
    actor_user_id TEXT,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_object_activity_created_at ON object_activity(created_at DESC, id DESC);
CREATE INDEX idx_object_activity_bucket ON object_activity(bucket_name, created_at DESC);
`,
	},
	{
		version: 4,
		name:    "add multipart content_type",
		run: func(tx *sql.Tx) error {
			var count int
			err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('multipart_uploads') WHERE name = 'content_type'`).Scan(&count)
			if err != nil {
				return err
			}
			if count > 0 {
				return nil
			}
			_, err = tx.Exec(`ALTER TABLE multipart_uploads ADD COLUMN content_type TEXT NOT NULL DEFAULT 'application/octet-stream'`)
			return err
		},
	},
	{
		version: 5,
		name:    "add multipart upload status",
		run: func(tx *sql.Tx) error {
			var count int
			err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('multipart_uploads') WHERE name = 'status'`).Scan(&count)
			if err != nil {
				return err
			}
			if count > 0 {
				return nil
			}
			_, err = tx.Exec(`ALTER TABLE multipart_uploads ADD COLUMN status TEXT NOT NULL DEFAULT 'active'`)
			return err
		},
	},
	{
		version: 6,
		name:    "add multipart status updated timestamp",
		run: func(tx *sql.Tx) error {
			var count int
			err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('multipart_uploads') WHERE name = 'status_updated_at'`).Scan(&count)
			if err != nil {
				return err
			}
			if count > 0 {
				return nil
			}
			_, err = tx.Exec(`ALTER TABLE multipart_uploads ADD COLUMN status_updated_at TIMESTAMP`)
			if err != nil {
				return err
			}
			_, err = tx.Exec(`UPDATE multipart_uploads SET status_updated_at = CURRENT_TIMESTAMP WHERE status_updated_at IS NULL`)
			return err
		},
	},
	{
		version: 7,
		name:    "validate multipart upload status",
		sql: `
CREATE TRIGGER IF NOT EXISTS validate_multipart_upload_status_insert
BEFORE INSERT ON multipart_uploads
FOR EACH ROW
WHEN NEW.status NOT IN ('active', 'completing', 'aborted')
BEGIN
    SELECT RAISE(ABORT, 'invalid multipart upload status');
END;

CREATE TRIGGER IF NOT EXISTS validate_multipart_upload_status_update
BEFORE UPDATE OF status ON multipart_uploads
FOR EACH ROW
WHEN NEW.status NOT IN ('active', 'completing', 'aborted')
BEGIN
    SELECT RAISE(ABORT, 'invalid multipart upload status');
END;
`,
	},
	{
		version: 8,
		name:    "add resource grants",
		sql: `
CREATE TABLE IF NOT EXISTS grants (
    id              TEXT PRIMARY KEY,
    bucket_name     TEXT NOT NULL REFERENCES buckets(name) ON DELETE CASCADE,
    grantee_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action          TEXT NOT NULL,
    key_prefix      TEXT NOT NULL DEFAULT '',
    is_active       INTEGER NOT NULL DEFAULT 1,
    created_by      TEXT REFERENCES users(id) ON DELETE SET NULL,
    note            TEXT,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_grants_unique_active
    ON grants(bucket_name, grantee_user_id, action, key_prefix)
    WHERE is_active = 1;

CREATE INDEX IF NOT EXISTS idx_grants_bucket_grantee
    ON grants(bucket_name, grantee_user_id);

CREATE INDEX IF NOT EXISTS idx_grants_grantee
    ON grants(grantee_user_id);

CREATE INDEX IF NOT EXISTS idx_grants_bucket
    ON grants(bucket_name);
`,
	},
	{
		version: 9,
		name:    "add multipart part checksums",
		run: func(tx *sql.Tx) error {
			for _, col := range []string{"checksum_crc32", "checksum_crc32c", "checksum_crc64nvme", "checksum_sha1", "checksum_sha256"} {
				if err := addColumnIfMissing(tx, "multipart_parts", col, "TEXT", ""); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 10,
		name:    "add object is_multipart and checksums",
		run: func(tx *sql.Tx) error {
			for _, col := range []struct{ name, typ, def string }{
				{"is_multipart", "INTEGER", "0"},
				{"checksum_crc32", "TEXT", ""},
				{"checksum_crc32c", "TEXT", ""},
				{"checksum_crc64nvme", "TEXT", ""},
				{"checksum_sha1", "TEXT", ""},
				{"checksum_sha256", "TEXT", ""},
			} {
				if err := addColumnIfMissing(tx, "objects", col.name, col.typ, col.def); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 11,
		name:    "add checksum_algorithm, parts_count, user_metadata",
		run: func(tx *sql.Tx) error {
			for _, col := range []struct{ name, typ, def string }{
				{"checksum_algorithm", "TEXT", ""},
				{"user_metadata", "TEXT", ""},
			} {
				if err := addColumnIfMissing(tx, "multipart_uploads", col.name, col.typ, col.def); err != nil {
					return err
				}
			}
			for _, col := range []struct{ name, typ, def string }{
				{"parts_count", "INTEGER", "0"},
				{"user_metadata", "TEXT", ""},
			} {
				if err := addColumnIfMissing(tx, "objects", col.name, col.typ, col.def); err != nil {
					return err
				}
			}
			return nil
		},
	},
}

// addColumnIfMissing adds a column to a table if it doesn't already exist.
func addColumnIfMissing(tx *sql.Tx, table, name, typ, def string) error {
	var count int
	err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('`+table+`') WHERE name = ?`, name).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	sql := `ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + typ
	if def != "" {
		sql += ` NOT NULL DEFAULT ` + def
	}
	if _, err := tx.Exec(sql); err != nil {
		return err
	}
	return nil
}

func ensureMigrationsTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name    TEXT NOT NULL,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

func getAppliedVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

// runMigration executes a single migration inside a transaction and records it.
// If any statement fails, the entire migration is rolled back.
func runMigration(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var execErr error
	if m.run != nil {
		execErr = m.run(tx)
	} else {
		_, execErr = tx.Exec(m.sql)
	}
	if execErr != nil {
		return fmt.Errorf("exec migration: %w", execErr)
	}

	if _, err := tx.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}

	return nil
}

// Run executes schema migrations sequentially on the provided db.
// It is safe to call multiple times; already-applied migrations are skipped.
func Run(db *sql.DB) error {
	return runMigrations(db, migrations)
}

func runMigrations(db *sql.DB, migs []migration) error {
	if err := ensureMigrationsTable(db); err != nil {
		return fmt.Errorf("ensure migrations table: %w", err)
	}

	appliedVersion, err := getAppliedVersion(db)
	if err != nil {
		return fmt.Errorf("get applied version: %w", err)
	}

	for _, m := range migs {
		if m.version <= appliedVersion {
			continue
		}

		if err := runMigration(db, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}

	return nil
}
