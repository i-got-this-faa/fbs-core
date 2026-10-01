package metadata

// rowScanner is satisfied by *sql.Row and *sql.Rows, so one scan function per
// type serves both single-row lookups and list iteration.
type rowScanner interface {
	Scan(dest ...any) error
}
