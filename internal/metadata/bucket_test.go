package metadata

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// createBucketsTable is the DDL for the buckets table used in bucket tests.
// The users table is also required because owner_id is a foreign key.
const createBucketsTable = `
CREATE TABLE IF NOT EXISTS buckets (
    name       TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL REFERENCES users(id),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

// openTestDBWithBuckets opens an in-memory SQLite DB with both users and buckets tables
// and enables foreign key enforcement.
func openTestDBWithBuckets(t *testing.T) *sql.DB {
	t.Helper()

	db := openTestDB(t) // creates the users table

	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign_keys: %v", err)
	}

	if _, err := db.Exec(createBucketsTable); err != nil {
		t.Fatalf("create buckets table: %v", err)
	}

	return db
}

// insertTestUser inserts a minimal user row so bucket foreign-key constraints pass.
func insertTestUser(t *testing.T, db *sql.DB) string {
	t.Helper()

	repo := NewUserRepository(db)
	u := newTestUser()
	if err := repo.Create(context.Background(), u); err != nil {
		t.Fatalf("insertTestUser: %v", err)
	}

	return u.ID
}

// newTestBucket returns a Bucket with sensible defaults for testing.
func newTestBucket(ownerID string) *Bucket {
	return &Bucket{
		Name:      "test-bucket-" + uuid.NewString()[:8],
		OwnerID:   ownerID,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
}

func TestBucketCreate(t *testing.T) {
	db := openTestDBWithBuckets(t)
	repo := NewBucketRepository(db)
	ctx := context.Background()

	ownerID := insertTestUser(t, db)
	b := newTestBucket(ownerID)

	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestBucketCreate_DuplicateName(t *testing.T) {
	db := openTestDBWithBuckets(t)
	repo := NewBucketRepository(db)
	ctx := context.Background()

	ownerID := insertTestUser(t, db)
	b := newTestBucket(ownerID)

	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Same name — must violate PRIMARY KEY constraint.
	duplicate := *b
	if err := repo.Create(ctx, &duplicate); err == nil {
		t.Fatal("expected error on duplicate bucket name, got nil")
	}
}

func TestBucketCreate_InvalidOwner(t *testing.T) {
	db := openTestDBWithBuckets(t)
	repo := NewBucketRepository(db)
	ctx := context.Background()

	b := newTestBucket("nonexistent-user-id")
	if err := repo.Create(ctx, b); err == nil {
		t.Fatal("expected foreign-key error for invalid owner_id, got nil")
	}
}

func TestBucketGetByName(t *testing.T) {
	db := openTestDBWithBuckets(t)
	repo := NewBucketRepository(db)
	ctx := context.Background()

	ownerID := insertTestUser(t, db)
	want := newTestBucket(ownerID)

	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByName(ctx, want.Name)
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}

	assertBucketsEqual(t, want, got)
}

func TestBucketGetByName_NotFound(t *testing.T) {
	db := openTestDBWithBuckets(t)
	repo := NewBucketRepository(db)
	ctx := context.Background()

	_, err := repo.GetByName(ctx, "no-such-bucket")
	if !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("expected ErrBucketNotFound, got %v", err)
	}
}

func TestBucketList(t *testing.T) {
	db := openTestDBWithBuckets(t)
	repo := NewBucketRepository(db)
	ctx := context.Background()

	// Empty list should return no error and zero items.
	buckets, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List (empty): %v", err)
	}
	if len(buckets) != 0 {
		t.Fatalf("expected 0 buckets, got %d", len(buckets))
	}

	// Insert two buckets under the same owner.
	ownerID := insertTestUser(t, db)
	b1 := newTestBucket(ownerID)
	b2 := newTestBucket(ownerID)

	for _, b := range []*Bucket{b1, b2} {
		if err := repo.Create(ctx, b); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	buckets, err = repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(buckets))
	}
}

func TestBucketDelete(t *testing.T) {
	db := openTestDBWithBuckets(t)
	repo := NewBucketRepository(db)
	ctx := context.Background()

	ownerID := insertTestUser(t, db)
	b := newTestBucket(ownerID)

	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, b.Name); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Should no longer exist.
	if _, err := repo.GetByName(ctx, b.Name); !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("expected ErrBucketNotFound after Delete, got %v", err)
	}
}

func TestBucketDelete_NotFound(t *testing.T) {
	db := openTestDBWithBuckets(t)
	repo := NewBucketRepository(db)
	ctx := context.Background()

	if err := repo.Delete(ctx, "no-such-bucket"); !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("expected ErrBucketNotFound, got %v", err)
	}
}

func TestBucketCRUD_RoundTrip(t *testing.T) {
	db := openTestDBWithBuckets(t)
	repo := NewBucketRepository(db)
	ctx := context.Background()

	ownerID := insertTestUser(t, db)
	want := newTestBucket(ownerID)

	// Create
	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Read
	got, err := repo.GetByName(ctx, want.Name)
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	assertBucketsEqual(t, want, got)

	// List
	buckets, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket in List, got %d", len(buckets))
	}

	// Delete
	if err := repo.Delete(ctx, want.Name); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := repo.GetByName(ctx, want.Name); !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("expected ErrBucketNotFound after delete, got %v", err)
	}
}

// assertBucketsEqual compares two Buckets for field equality.
func assertBucketsEqual(t *testing.T, want, got *Bucket) {
	t.Helper()

	if got.Name != want.Name {
		t.Errorf("Name: want %q, got %q", want.Name, got.Name)
	}
	if got.OwnerID != want.OwnerID {
		t.Errorf("OwnerID: want %q, got %q", want.OwnerID, got.OwnerID)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt: want %v, got %v", want.CreatedAt, got.CreatedAt)
	}
}
