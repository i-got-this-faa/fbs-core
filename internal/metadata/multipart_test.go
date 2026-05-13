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
    id           TEXT PRIMARY KEY,
    bucket_name  TEXT NOT NULL REFERENCES buckets(name),
    key          TEXT NOT NULL,
	    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
	    status       TEXT NOT NULL DEFAULT 'active',
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
`

func openTestDBWithMultiparts(t *testing.T) *sql.DB {
	t.Helper()

	db := openTestDBWithBuckets(t) // creates users and buckets tables

	if _, err := db.Exec(createMultipartTables); err != nil {
		t.Fatalf("create multipart tables: %v", err)
	}
	if _, err := db.Exec(createObjectsTable); err != nil {
		t.Fatalf("create objects table: %v", err)
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
	now := time.Now().UTC().Truncate(time.Second)
	return &MultipartUpload{
		ID:              uuid.NewString(),
		BucketName:      bucketName,
		Key:             "test-key-" + uuid.NewString()[:8],
		ContentType:     "application/octet-stream",
		Status:          "active",
		CreatedAt:       now,
		StatusUpdatedAt: now,
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

	if got.ID != want.ID || got.BucketName != want.BucketName || got.Key != want.Key || got.ContentType != want.ContentType || got.Status != want.Status || !got.CreatedAt.Equal(want.CreatedAt) {
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
	cutoff := time.Now().UTC().Add(-24 * time.Hour)

	// Create old upload
	oldUpload := newTestMultipartUpload(bucketName)
	oldUpload.CreatedAt = time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	if err := repo.Create(ctx, oldUpload); err != nil {
		t.Fatalf("Create old upload: %v", err)
	}

	// Create old claimed upload. These are treated as abandoned claims and are
	// eligible for stale cleanup once their status timestamp is old enough.
	oldClaimedUpload := newTestMultipartUpload(bucketName)
	oldClaimedUpload.Status = MultipartUploadStatusCompleting
	oldClaimedUpload.StatusUpdatedAt = time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	if err := repo.Create(ctx, oldClaimedUpload); err != nil {
		t.Fatalf("Create old claimed upload: %v", err)
	}

	// Create recent upload
	recentUpload := newTestMultipartUpload(bucketName)
	recentUpload.CreatedAt = time.Now().UTC().Truncate(time.Second)
	if err := repo.Create(ctx, recentUpload); err != nil {
		t.Fatalf("Create recent upload: %v", err)
	}

	recentClaimedUpload := newTestMultipartUpload(bucketName)
	recentClaimedUpload.Status = MultipartUploadStatusCompleting
	recentClaimedUpload.CreatedAt = time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	recentClaimedUpload.StatusUpdatedAt = time.Now().UTC().Truncate(time.Second)
	if err := repo.Create(ctx, recentClaimedUpload); err != nil {
		t.Fatalf("Create recent claimed upload: %v", err)
	}

	stale, err := repo.ListStale(ctx, cutoff)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}

	if len(stale) != 2 {
		t.Fatalf("expected 2 stale uploads, got %d", len(stale))
	}
	got := map[string]bool{}
	for _, upload := range stale {
		got[upload.ID] = true
	}
	if !got[oldUpload.ID] {
		t.Errorf("old active upload was not stale")
	}
	if !got[oldClaimedUpload.ID] {
		t.Errorf("old claimed upload was not stale")
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
	if _, err := repo.AddPart(ctx, part1); err != nil {
		t.Fatalf("AddPart 1: %v", err)
	}

	part2 := newTestMultipartPart(upload.ID, 2)
	if _, err := repo.AddPart(ctx, part2); err != nil {
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
	if _, err := repo.AddPart(ctx, part); err != nil {
		t.Fatalf("AddPart initial: %v", err)
	}

	part.Size = 2048
	part.ETag = "updated-etag"
	part.StoragePath = "data/replaced"
	part.CreatedAt = part.CreatedAt.Add(time.Minute)
	if _, err := repo.AddPart(ctx, part); err != nil {
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
	if _, err := repo.AddPart(ctx, part); err != nil {
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

func TestMultipartUploadCompleteUpload(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	objRepo := NewObjectRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	upload := newTestMultipartUpload(bucketName)
	if err := repo.Create(ctx, upload); err != nil {
		t.Fatalf("Create upload: %v", err)
	}

	part := newTestMultipartPart(upload.ID, 1)
	if _, err := repo.AddPart(ctx, part); err != nil {
		t.Fatalf("AddPart: %v", err)
	}

	obj := &Object{
		ID:          uuid.NewString(),
		BucketName:  bucketName,
		Key:         upload.Key,
		Size:        1024,
		ETag:        "test-etag",
		ContentType: "application/octet-stream",
		StoragePath: "data/new-object",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	if err := repo.ClaimUpload(ctx, upload.ID, "completing"); err != nil {
		t.Fatalf("ClaimUpload: %v", err)
	}

	oldPath, err := repo.CompleteUpload(ctx, obj, upload.ID)
	if err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	if oldPath != "" {
		t.Fatalf("expected empty old path for new object, got %q", oldPath)
	}

	// Verify upload is deleted.
	if _, err := repo.GetByID(ctx, upload.ID); !errors.Is(err, ErrMultipartUploadNotFound) {
		t.Fatalf("expected upload deleted, got %v", err)
	}

	// Verify object exists.
	got, err := objRepo.GetByKey(ctx, bucketName, upload.Key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if got.StoragePath != obj.StoragePath {
		t.Errorf("StoragePath = %q, want %q", got.StoragePath, obj.StoragePath)
	}
}

func TestMultipartUploadCompleteUpload_OverwriteExistingObject(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	objRepo := NewObjectRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)

	upload := newTestMultipartUpload(bucketName)
	if err := repo.Create(ctx, upload); err != nil {
		t.Fatalf("Create upload: %v", err)
	}

	// Create an existing object with the same key.
	oldObj := newTestObject(bucketName, upload.Key)
	oldObj.StoragePath = "data/old-object"
	if err := objRepo.Create(ctx, oldObj); err != nil {
		t.Fatalf("Create old object: %v", err)
	}

	obj := &Object{
		ID:          uuid.NewString(),
		BucketName:  bucketName,
		Key:         upload.Key,
		Size:        2048,
		ETag:        "new-etag",
		ContentType: "application/octet-stream",
		StoragePath: "data/new-object",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	if err := repo.ClaimUpload(ctx, upload.ID, "completing"); err != nil {
		t.Fatalf("ClaimUpload: %v", err)
	}

	oldPath, err := repo.CompleteUpload(ctx, obj, upload.ID)
	if err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	if oldPath != oldObj.StoragePath {
		t.Errorf("oldPath = %q, want %q", oldPath, oldObj.StoragePath)
	}

	got, err := objRepo.GetByKey(ctx, bucketName, upload.Key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if got.StoragePath != obj.StoragePath {
		t.Errorf("StoragePath = %q, want %q", got.StoragePath, obj.StoragePath)
	}
}

func TestMultipartUploadCompleteUpload_NotFound(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	obj := &Object{
		ID:          uuid.NewString(),
		BucketName:  bucketName,
		Key:         "test-key",
		Size:        1024,
		ETag:        "test-etag",
		ContentType: "application/octet-stream",
		StoragePath: "data/object",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	_, err := repo.CompleteUpload(ctx, obj, "nonexistent-upload")
	if !errors.Is(err, ErrMultipartUploadNotFound) {
		t.Fatalf("expected ErrMultipartUploadNotFound, got %v", err)
	}
}

func TestMultipartUploadCompleteUpload_RejectNonCompleting(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	objRepo := NewObjectRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	upload := newTestMultipartUpload(bucketName)
	if err := repo.Create(ctx, upload); err != nil {
		t.Fatalf("Create upload: %v", err)
	}

	part := newTestMultipartPart(upload.ID, 1)
	if _, err := repo.AddPart(ctx, part); err != nil {
		t.Fatalf("AddPart: %v", err)
	}

	obj := &Object{
		ID:          uuid.NewString(),
		BucketName:  bucketName,
		Key:         upload.Key,
		Size:        1024,
		ETag:        "test-etag",
		ContentType: "application/octet-stream",
		StoragePath: "data/new-object",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	// CompleteUpload should reject an active upload (not claimed as completing).
	_, err := repo.CompleteUpload(ctx, obj, upload.ID)
	if !errors.Is(err, ErrUploadAlreadyClaimed) {
		t.Fatalf("expected ErrUploadAlreadyClaimed, got %v", err)
	}

	// After claiming, it should succeed.
	if err := repo.ClaimUpload(ctx, upload.ID, "completing"); err != nil {
		t.Fatalf("ClaimUpload: %v", err)
	}
	oldPath, err := repo.CompleteUpload(ctx, obj, upload.ID)
	if err != nil {
		t.Fatalf("CompleteUpload after claim: %v", err)
	}
	if oldPath != "" {
		t.Fatalf("expected empty old path for new object, got %q", oldPath)
	}

	got, err := objRepo.GetByKey(ctx, bucketName, upload.Key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if got.StoragePath != obj.StoragePath {
		t.Errorf("StoragePath = %q, want %q", got.StoragePath, obj.StoragePath)
	}
}

func TestMultipartUploadSetUploadStatus(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	upload := newTestMultipartUpload(bucketName)
	if err := repo.Create(ctx, upload); err != nil {
		t.Fatalf("Create upload: %v", err)
	}

	if err := repo.SetUploadStatus(ctx, upload.ID, "aborted"); err != nil {
		t.Fatalf("SetUploadStatus: %v", err)
	}

	got, err := repo.GetByID(ctx, upload.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != "aborted" {
		t.Errorf("Status = %q, want aborted", got.Status)
	}

	// Reset to active.
	if err := repo.SetUploadStatus(ctx, upload.ID, "active"); err != nil {
		t.Fatalf("SetUploadStatus reset: %v", err)
	}
	got, err = repo.GetByID(ctx, upload.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != "active" {
		t.Errorf("Status = %q, want active", got.Status)
	}
}

func TestMultipartUploadSetUploadStatus_NotFound(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	if err := repo.SetUploadStatus(ctx, "nonexistent", "active"); !errors.Is(err, ErrMultipartUploadNotFound) {
		t.Fatalf("expected ErrMultipartUploadNotFound, got %v", err)
	}
}

func TestMultipartUploadStatusValidation(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	upload := newTestMultipartUpload(bucketName)
	upload.Status = "typo"
	if err := repo.Create(ctx, upload); err == nil {
		t.Fatal("Create invalid status error = nil, want error")
	}

	upload = newTestMultipartUpload(bucketName)
	if err := repo.Create(ctx, upload); err != nil {
		t.Fatalf("Create upload: %v", err)
	}
	if err := repo.ClaimUpload(ctx, upload.ID, "typo"); err == nil {
		t.Fatal("ClaimUpload invalid status error = nil, want error")
	}
	if err := repo.SetUploadStatus(ctx, upload.ID, "typo"); err == nil {
		t.Fatal("SetUploadStatus invalid status error = nil, want error")
	}
}

func TestMultipartUploadClaim(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	upload := newTestMultipartUpload(bucketName)
	if err := repo.Create(ctx, upload); err != nil {
		t.Fatalf("Create upload: %v", err)
	}

	if err := repo.ClaimUpload(ctx, upload.ID, "completing"); err != nil {
		t.Fatalf("ClaimUpload: %v", err)
	}

	got, err := repo.GetByID(ctx, upload.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != "completing" {
		t.Errorf("Status = %q, want completing", got.Status)
	}

	// Second claim should fail.
	if err := repo.ClaimUpload(ctx, upload.ID, "aborted"); !errors.Is(err, ErrUploadAlreadyClaimed) {
		t.Fatalf("expected ErrUploadAlreadyClaimed, got %v", err)
	}
}

func TestMultipartUploadClaim_NotFound(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	if err := repo.ClaimUpload(ctx, "nonexistent", "completing"); !errors.Is(err, ErrMultipartUploadNotFound) {
		t.Fatalf("expected ErrMultipartUploadNotFound, got %v", err)
	}
}

func TestMultipartPartAdd_RejectedWhenClaimed(t *testing.T) {
	db := openTestDBWithMultiparts(t)
	repo := NewMultipartUploadRepository(db)
	ctx := context.Background()

	bucketName := insertTestBucket(t, db)
	upload := newTestMultipartUpload(bucketName)
	if err := repo.Create(ctx, upload); err != nil {
		t.Fatalf("Create upload: %v", err)
	}

	if err := repo.ClaimUpload(ctx, upload.ID, "completing"); err != nil {
		t.Fatalf("ClaimUpload: %v", err)
	}

	part := newTestMultipartPart(upload.ID, 1)
	_, err := repo.AddPart(ctx, part)
	if !errors.Is(err, ErrUploadAlreadyClaimed) {
		t.Fatalf("expected ErrUploadAlreadyClaimed, got %v", err)
	}
}
