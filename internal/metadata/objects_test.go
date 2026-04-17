package metadata

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

const createObjectsTable = `
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
`

func openTestDBWithObjects(t *testing.T) *sql.DB {
	t.Helper()

	db := openTestDBWithBuckets(t) // creates users and buckets tables

	if _, err := db.Exec(createObjectsTable); err != nil {
		t.Fatalf("create objects table: %v", err)
	}

	return db
}

func newTestObject(bucketName, key string) *Object {
	return &Object{
		ID:          uuid.NewString(),
		BucketName:  bucketName,
		Key:         key,
		Size:        2048,
		ETag:        "8843d7f92416211de9ebb963ff4ce281",
		ContentType: "text/plain",
		StoragePath: "data/" + uuid.NewString(),
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
		UpdatedAt:   time.Now().UTC().Truncate(time.Second),
	}
}

func TestObjectCreateAndGet(t *testing.T) {
	db := openTestDBWithObjects(t)
	repo := NewObjectRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	want := newTestObject(bucketName, "docs/readme.md")

	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByKey(ctx, bucketName, want.Key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}

	if got.ID != want.ID || got.Key != want.Key || got.Size != want.Size {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestObjectUpsert(t *testing.T) {
	db := openTestDBWithObjects(t)
	repo := NewObjectRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	obj := newTestObject(bucketName, "docs/readme.md")

	if err := repo.Create(ctx, obj); err != nil {
		t.Fatalf("Create (first): %v", err)
	}

	// Change some fields and create again (should update)
	updatedID := uuid.NewString()
	updatedCreatedAt := obj.CreatedAt.Add(time.Hour)
	obj.ID = updatedID
	obj.Size = 4096
	obj.ETag = "new-etag"
	obj.CreatedAt = updatedCreatedAt
	if err := repo.Create(ctx, obj); err != nil {
		t.Fatalf("Create (upsert): %v", err)
	}

	got, err := repo.GetByKey(ctx, bucketName, obj.Key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}

	if got.Size != 4096 || got.ETag != "new-etag" {
		t.Errorf("upsert failed, got size=%d, etag=%s", got.Size, got.ETag)
	}
	if got.ID != updatedID {
		t.Errorf("upsert failed to replace id, got %s want %s", got.ID, updatedID)
	}
	if !got.CreatedAt.Equal(updatedCreatedAt) {
		t.Errorf("upsert failed to replace created_at, got %v want %v", got.CreatedAt, updatedCreatedAt)
	}
}

func TestObjectDelete(t *testing.T) {
	db := openTestDBWithObjects(t)
	repo := NewObjectRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	obj := newTestObject(bucketName, "docs/readme.md")

	if err := repo.Create(ctx, obj); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, bucketName, obj.Key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := repo.GetByKey(ctx, bucketName, obj.Key); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("expected ErrObjectNotFound, got %v", err)
	}
}

func TestObjectDeleteAllInBucket(t *testing.T) {
	db := openTestDBWithObjects(t)
	repo := NewObjectRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	obj1 := newTestObject(bucketName, "file1.txt")
	obj2 := newTestObject(bucketName, "file2.txt")

	repo.Create(ctx, obj1)
	repo.Create(ctx, obj2)

	if err := repo.DeleteAllInBucket(ctx, bucketName); err != nil {
		t.Fatalf("DeleteAllInBucket: %v", err)
	}

	if _, err := repo.GetByKey(ctx, bucketName, obj1.Key); err == nil {
		t.Fatal("expected obj1 to be deleted")
	}
}

func TestObjectList(t *testing.T) {
	db := openTestDBWithObjects(t)
	repo := NewObjectRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)

	objects := []string{
		"a.txt",
		"b/1.txt",
		"b/2.txt",
		"c.txt",
	}

	for _, key := range objects {
		obj := newTestObject(bucketName, key)
		if err := repo.Create(ctx, obj); err != nil {
			t.Fatalf("Create %s: %v", key, err)
		}
	}

	// 1. List with prefix
	results, isTruncated, err := repo.List(ctx, bucketName, "b/", "", 10)
	if err != nil {
		t.Fatalf("List prefix: %v", err)
	}
	if len(results) != 2 || isTruncated {
		t.Errorf("expected 2 items, not truncated, got %d items, truncated=%v", len(results), isTruncated)
	}

	// 2. List with pagination
	results, isTruncated, err = repo.List(ctx, bucketName, "", "", 2)
	if err != nil {
		t.Fatalf("List pagination page 1: %v", err)
	}
	if len(results) != 2 || !isTruncated {
		t.Errorf("expected 2 items and truncated, got %d items, truncated=%v", len(results), isTruncated)
	}
	if results[0].Key != "a.txt" || results[1].Key != "b/1.txt" {
		t.Errorf("unexpected results: %v", results)
	}

	// Next page
	lastItem := results[1].Key
	results2, isTruncated2, err := repo.List(ctx, bucketName, "", lastItem, 2)
	if err != nil {
		t.Fatalf("List pagination page 2: %v", err)
	}
	if len(results2) != 2 || isTruncated2 { // only 2 items left, shouldn't be truncated at max 2
		t.Errorf("expected 2 items and not truncated, got %d items, truncated=%v", len(results2), isTruncated2)
	}
	if results2[0].Key != "b/2.txt" || results2[1].Key != "c.txt" {
		t.Errorf("unexpected results: %v", results)
	}
}

func TestObjectList_PrefixWithWildcards(t *testing.T) {
	db := openTestDBWithObjects(t)
	repo := NewObjectRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	for _, key := range []string{"a%b.txt", "a_b.txt", "axb.txt"} {
		obj := newTestObject(bucketName, key)
		if err := repo.Create(ctx, obj); err != nil {
			t.Fatalf("Create %s: %v", key, err)
		}
	}

	results, isTruncated, err := repo.List(ctx, bucketName, "a%", "", 10)
	if err != nil {
		t.Fatalf("List wildcard prefix: %v", err)
	}
	if isTruncated {
		t.Fatal("expected wildcard prefix list to not be truncated")
	}
	if len(results) != 1 || results[0].Key != "a%b.txt" {
		t.Fatalf("unexpected wildcard prefix results: %+v", results)
	}

	results, isTruncated, err = repo.List(ctx, bucketName, "a_", "", 10)
	if err != nil {
		t.Fatalf("List underscore prefix: %v", err)
	}
	if isTruncated {
		t.Fatal("expected underscore prefix list to not be truncated")
	}
	if len(results) != 1 || results[0].Key != "a_b.txt" {
		t.Fatalf("unexpected underscore prefix results: %+v", results)
	}
}
