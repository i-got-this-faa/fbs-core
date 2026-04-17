package metadata

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOpenDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fbs.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal mode 'wal', got %q", journalMode)
	}

	ctx := context.Background()
	conn1, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn 1: %v", err)
	}
	defer conn1.Close()

	conn2, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn 2: %v", err)
	}
	defer conn2.Close()

	assertConnectionPragmas(t, conn1)
	assertConnectionPragmas(t, conn2)
}

func assertConnectionPragmas(t *testing.T, conn interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) {
	t.Helper()

	ctx := context.Background()
	var busyTimeout int
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("expected busy timeout 5000, got %d", busyTimeout)
	}

	var foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("expected foreign_keys = 1, got %d", foreignKeys)
	}
}

func TestMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fbs_migrate.db")

	// First run creates tables
	db1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open failed: %v", err)
	}
	db1.Close()

	// Second run should be idempotent
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open failed: %v", err)
	}
	defer db2.Close()

	// Check tables exist
	var count int
	err = db2.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('users', 'buckets', 'objects', 'multipart_uploads', 'multipart_parts')").Scan(&count)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5 metadata tables to be created, got %d", count)
	}
}

func TestListStaleUpsert(t *testing.T) {
	// A combined test checking the stale logic for multiparts and upsert for objects as requested
	dbPath := filepath.Join(t.TempDir(), "fbs_mixed.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	userRepo := NewUserRepository(db)
	bucketRepo := NewBucketRepository(db)
	objRepo := NewObjectRepository(db)
	mpRepo := NewMultipartUploadRepository(db)

	u := newTestUser()
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("create user: %v", err)
	}

	b := &Bucket{
		Name:      "test-bucket",
		OwnerID:   u.ID,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := bucketRepo.Create(ctx, b); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	// 1. Test Object Upsert
	obj := &Object{
		ID:          uuid.NewString(),
		BucketName:  b.Name,
		Key:         "upsert-key",
		Size:        100,
		ETag:        "v1",
		ContentType: "text/plain",
		StoragePath: "path1",
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
	}
	if err := objRepo.Create(ctx, obj); err != nil {
		t.Fatalf("obj create v1: %v", err)
	}

	obj.Size = 200
	obj.ETag = "v2"
	obj.ID = uuid.NewString()
	obj.CreatedAt = obj.CreatedAt.Add(time.Hour)
	if err := objRepo.Create(ctx, obj); err != nil {
		t.Fatalf("obj create v2 (upsert): %v", err)
	}

	gotObj, err := objRepo.GetByKey(ctx, b.Name, obj.Key)
	if err != nil {
		t.Fatalf("obj get: %v", err)
	}
	if gotObj.Size != 200 || gotObj.ETag != "v2" {
		t.Errorf("expected upserted size 200/etag v2, got size %d/etag %v", gotObj.Size, gotObj.ETag)
	}
	if gotObj.ID != obj.ID {
		t.Errorf("expected upserted id %s, got %s", obj.ID, gotObj.ID)
	}

	// 2. Test List Stale
	staleUpload := &MultipartUpload{
		ID:         uuid.NewString(),
		BucketName: b.Name,
		Key:        "stale-key",
		CreatedAt:  time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second),
	}
	if err := mpRepo.Create(ctx, staleUpload); err != nil {
		t.Fatalf("create stale mp: %v", err)
	}

	recentUpload := &MultipartUpload{
		ID:         uuid.NewString(),
		BucketName: b.Name,
		Key:        "recent-key",
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
	}
	if err := mpRepo.Create(ctx, recentUpload); err != nil {
		t.Fatalf("create recent mp: %v", err)
	}

	staleList, err := mpRepo.ListStale(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("list stale: %v", err)
	}
	if len(staleList) != 1 || staleList[0].ID != staleUpload.ID {
		t.Errorf("expected 1 stale upload with ID %s, got %v", staleUpload.ID, staleList)
	}
}
