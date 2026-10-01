package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ShareLink is a short, revocable alias that resolves to an object by
// bucket and key. It is an fbs extension and is not part of the S3 API.
type ShareLink struct {
	Code                       string
	BucketName                 string
	ObjectKey                  string
	ResponseContentDisposition string
	CreatedBy                  string
	ExpiresAt                  *time.Time
	CreatedAt                  time.Time
}

// IsExpired reports whether the link has an expiry at or before now.
func (l ShareLink) IsExpired(now time.Time) bool {
	return l.ExpiresAt != nil && !now.Before(*l.ExpiresAt)
}

// ErrShareLinkNotFound is returned when a share link lookup yields no rows.
var ErrShareLinkNotFound = errors.New("share link not found")

// ErrShareLinkCodeTaken is returned when a share link code is already in use.
var ErrShareLinkCodeTaken = errors.New("share link code already exists")

// ShareLinkListFilter narrows share link listings. Empty fields match all.
type ShareLinkListFilter struct {
	BucketName string
}

// ShareLinkRepository persists share links.
type ShareLinkRepository interface {
	Create(ctx context.Context, link *ShareLink) error
	GetByCode(ctx context.Context, code string) (*ShareLink, error)
	List(ctx context.Context, filter ShareLinkListFilter) ([]ShareLink, error)
	Delete(ctx context.Context, code string) error
}

type sqliteShareLinkRepository struct {
	db *sql.DB
}

// NewShareLinkRepository returns a ShareLinkRepository backed by the given *sql.DB.
func NewShareLinkRepository(db *sql.DB) ShareLinkRepository {
	return &sqliteShareLinkRepository{db: db}
}

const shareLinkColumns = `code, bucket_name, object_key, response_content_disposition, created_by, expires_at, created_at`

func (r *sqliteShareLinkRepository) Create(ctx context.Context, link *ShareLink) error {
	const q = `INSERT INTO share_links (` + shareLinkColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?)`

	var expiresAt sql.NullTime
	if link.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: link.ExpiresAt.UTC(), Valid: true}
	}

	_, err := r.db.ExecContext(ctx, q,
		link.Code,
		link.BucketName,
		link.ObjectKey,
		link.ResponseContentDisposition,
		link.CreatedBy,
		expiresAt,
		link.CreatedAt.UTC(),
	)
	if isUniqueConstraintError(err) {
		return ErrShareLinkCodeTaken
	}
	if err != nil {
		return fmt.Errorf("create share link: %w", err)
	}
	return nil
}

func (r *sqliteShareLinkRepository) GetByCode(ctx context.Context, code string) (*ShareLink, error) {
	const q = `SELECT ` + shareLinkColumns + ` FROM share_links WHERE code = ?`

	link, err := scanShareLink(r.db.QueryRowContext(ctx, q, code))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrShareLinkNotFound
	}
	if err != nil {
		return nil, err
	}
	return &link, nil
}

func (r *sqliteShareLinkRepository) List(ctx context.Context, filter ShareLinkListFilter) ([]ShareLink, error) {
	const q = `SELECT ` + shareLinkColumns + ` FROM share_links
WHERE (? = '' OR bucket_name = ?)
ORDER BY created_at DESC, code`

	rows, err := r.db.QueryContext(ctx, q, filter.BucketName, filter.BucketName)
	if err != nil {
		return nil, fmt.Errorf("list share links: %w", err)
	}
	defer rows.Close()

	links := []ShareLink{}
	for rows.Next() {
		link, err := scanShareLink(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list share links rows: %w", err)
	}
	return links, nil
}

func (r *sqliteShareLinkRepository) Delete(ctx context.Context, code string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM share_links WHERE code = ?`, code)
	if err != nil {
		return fmt.Errorf("delete share link: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete share link rows affected: %w", err)
	}
	if rows == 0 {
		return ErrShareLinkNotFound
	}
	return nil
}

func scanShareLink(row rowScanner) (ShareLink, error) {
	var link ShareLink
	var expiresAt sql.NullString
	var createdAt string

	err := row.Scan(
		&link.Code,
		&link.BucketName,
		&link.ObjectKey,
		&link.ResponseContentDisposition,
		&link.CreatedBy,
		&expiresAt,
		&createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ShareLink{}, err
	}
	if err != nil {
		return ShareLink{}, fmt.Errorf("scan share link: %w", err)
	}

	if expiresAt.Valid && expiresAt.String != "" {
		parsed, err := parseTimestamp(expiresAt.String)
		if err != nil {
			return ShareLink{}, err
		}
		link.ExpiresAt = &parsed
	}
	link.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return ShareLink{}, err
	}
	return link, nil
}
