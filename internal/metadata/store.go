package metadata

import (
	"database/sql"
	"fmt"

	"github.com/i-got-this-faa/fbs/migrations"
	_ "modernc.org/sqlite"
)

// Open opens the SQLite database at the given path, applies pragmas,
// runs migrations, and returns the *sql.DB.
func Open(dbPath string) (*sql.DB, error) {
	// 1. Open the DB
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	// 2. Apply pragmas
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA foreign_keys = ON;",
	}

	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("execute pragma %q: %w", p, err)
		}
	}

	// 3. Run schema migrations
	if err := migrations.Run(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrations run: %w", err)
	}

	return db, nil
}
