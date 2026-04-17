package metadata

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/i-got-this-faa/fbs/migrations"
	_ "modernc.org/sqlite"
)

var sqlitePragmas = []string{
	"journal_mode(WAL)",
	"busy_timeout(5000)",
	"synchronous(NORMAL)",
	"foreign_keys(ON)",
}

// Open opens the SQLite database at the given path, applies pragmas,
// runs migrations, and returns the *sql.DB.
func Open(dbPath string) (*sql.DB, error) {
	dsn, err := sqliteDSN(dbPath)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite db: %w", err)
	}

	if err := migrations.Run(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrations run: %w", err)
	}

	return db, nil
}

func sqliteDSN(dbPath string) (string, error) {
	trimmed := strings.TrimSpace(dbPath)
	if trimmed == "" {
		return "", fmt.Errorf("database path is required")
	}

	var uri *url.URL
	if strings.HasPrefix(trimmed, "file:") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return "", fmt.Errorf("parse sqlite uri: %w", err)
		}
		uri = parsed
	} else {
		absPath, err := filepath.Abs(trimmed)
		if err != nil {
			return "", fmt.Errorf("resolve database path: %w", err)
		}
		uri = &url.URL{Scheme: "file", Path: absPath}
	}

	query := uri.Query()
	for _, pragma := range sqlitePragmas {
		query.Add("_pragma", pragma)
	}
	uri.RawQuery = query.Encode()

	return uri.String(), nil
}
