package metadata

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

const createMultipartTables = `
CREATE TABLE IF NOT EXISTS multipart_uploads (
    id          TEXT PRIMARY KEY,
    bucket_name TEXT NOT NULL REFERENCES buckets(name),
    key         TEXT NOT NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
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
`

func openTestDBWithMultiparts(t *testing.T) *sql.DB {
	t.Helper()

	db := openTestDBWithBuckets(t) // creates users and buckets tables

	if _, err := db.Exec(createMultipartTables); err != nil {
		t.Fatalf("create multipart tables: %v", err)
	}

	return db
}

func insertTestBucket(t *testing.T, db *sql.DB) string {
	t.Helper()

	ownerID := insertTestUser(t, db)
	repo := NewBucketRepository(db)
	b := newTestBucket(ownerID)

	if err := repo.Create(context.Background(), b); err != nil {
		t.Fatalf("insertTestBucket: %v", err)
	}

	return b.Name
}

func newTestMultipartUpload(bucketName string) *MultipartUpload {
	return &MultipartUpload{
		ID:         uuid.NewString(),
		BucketName: bucketName,
		Key:        "test-key-" + uuid.NewString()[:8],
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
	}
}

func newTestMultipartPart(uploadID string, partNum int) *MultipartPart {
	return &MultipartPart{
		UploadID:    uploadID,
		PartNumber:  partNum,
		Size:        1024,
		ETag:        "d41d8cd98f00b204e9800998ecf8427e",
		StoragePath: "data/" + uuid.NewString(),
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
	}
}

func TestMultipartUploadCreateGet(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	want := newTestMultipartUpload(bucketName)

	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if got.ID != want.ID || got.BucketName != want.BucketName || got.Key != want.Key || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMultipartUploadDelete(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	upload := newTestMultipartUpload(bucketName)

	if err := repo.Create(ctx, upload); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, upload.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := repo.GetByID(ctx, upload.ID); !errors.Is(err, ErrMultipartUploadNotFound) {
		t.Fatalf("expected ErrMultipartUploadNotFound, got %v", err)
	}
}

func TestMultipartUploadListStale(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)

	// Create old upload
	oldUpload := newTestMultipartUpload(bucketName)
	oldUpload.CreatedAt = time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	if err := repo.Create(ctx, oldUpload); err != nil {
		t.Fatalf("Create old upload: %v", err)
	}

	// Create recent upload
	recentUpload := newTestMultipartUpload(bucketName)
	recentUpload.CreatedAt = time.Now().UTC().Truncate(time.Second)
	if err := repo.Create(ctx, recentUpload); err != nil {
		t.Fatalf("Create recent upload: %v", err)
	}

	stale, err := repo.ListStale(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}

	if len(stale) != 1 {
		t.Fatalf("expected 1 stale upload, got %d", len(stale))
	}
	if stale[0].ID != oldUpload.ID {
		t.Errorf("got stale upload ID %s, want %s", stale[0].ID, oldUpload.ID)
	}
}

func TestMultipartPartAddList(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	upload := newTestMultipartUpload(bucketName)
	if err := repo.Create(ctx, upload); err != nil {
		t.Fatalf("Create upload: %v", err)
	}

	part1 := newTestMultipartPart(upload.ID, 1)
	if err := repo.AddPart(ctx, part1); err != nil {
		t.Fatalf("AddPart 1: %v", err)
	}

	part2 := newTestMultipartPart(upload.ID, 2)
	if err := repo.AddPart(ctx, part2); err != nil {
		t.Fatalf("AddPart 2: %v", err)
	}

	parts, err := repo.ListParts(ctx, upload.ID)
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}

	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	if parts[0].PartNumber != 1 || parts[1].PartNumber != 2 {
		t.Errorf("parts not ordered correctly: %v", parts)
	}
}

func TestMultipartPartAdd_ReplacesExistingPart(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	upload := newTestMultipartUpload(bucketName)
	if err := repo.Create(ctx, upload); err != nil {
		t.Fatalf("Create upload: %v", err)
	}

	part := newTestMultipartPart(upload.ID, 1)
	if err := repo.AddPart(ctx, part); err != nil {
		t.Fatalf("AddPart initial: %v", err)
	}

	part.Size = 2048
	part.ETag = "updated-etag"
	part.StoragePath = "data/replaced"
	part.CreatedAt = part.CreatedAt.Add(time.Minute)
	if err := repo.AddPart(ctx, part); err != nil {
		t.Fatalf("AddPart replace: %v", err)
	}

	parts, err := repo.ListParts(ctx, upload.ID)
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].Size != 2048 || parts[0].ETag != "updated-etag" || parts[0].StoragePath != "data/replaced" {
		t.Fatalf("unexpected replaced part: %+v", parts[0])
	}
}

func TestMultipartDeleteCascades(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	upload := newTestMultipartUpload(bucketName)
	if err := repo.Create(ctx, upload); err != nil {
		t.Fatalf("Create upload: %v", err)
	}

	part := newTestMultipartPart(upload.ID, 1)
	if err := repo.AddPart(ctx, part); err != nil {
		t.Fatalf("AddPart: %v", err)
	}

	// Delete upload should cascade and delete parts
	if err := repo.Delete(ctx, upload.ID); err != nil {
		t.Fatalf("Delete upload: %v", err)
	}

	parts, err := repo.ListParts(ctx, upload.ID)
	if err != nil {
		t.Fatalf("ListParts after delete: %v", err)
	}
	if len(parts) != 0 {
		t.Fatalf("expected 0 parts after upload deletion, got %d", len(parts))
	}
}
