package metadata

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestManagementRepositoryMetrics(t *testing.T) {
	t.Parallel()

	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	seedManagementRepositoryData(t, ctx, db)

	metrics, err := NewManagementRepository(db).Metrics(ctx)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}

	if metrics.BucketCount != 2 {
		t.Fatalf("BucketCount = %d, want 2", metrics.BucketCount)
	}
	if metrics.ObjectCount != 3 {
		t.Fatalf("ObjectCount = %d, want 3", metrics.ObjectCount)
	}
	if metrics.TotalObjectBytes != 60 {
		t.Fatalf("TotalObjectBytes = %d, want 60", metrics.TotalObjectBytes)
	}
	if metrics.UserCount != 2 {
		t.Fatalf("UserCount = %d, want 2", metrics.UserCount)
	}
	if metrics.ActiveUserCount != 1 {
		t.Fatalf("ActiveUserCount = %d, want 1", metrics.ActiveUserCount)
	}
}

func TestManagementRepositoryListBucketSummaries(t *testing.T) {
	t.Parallel()

	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	seedManagementRepositoryData(t, ctx, db)

	summaries, err := NewManagementRepository(db).ListBucketSummaries(ctx)
	if err != nil {
		t.Fatalf("list summaries: %v", err)
	}

	if len(summaries) != 2 {
		t.Fatalf("len(summaries) = %d, want 2", len(summaries))
	}
	if summaries[0].Name != "photos" {
		t.Fatalf("first bucket = %q, want photos", summaries[0].Name)
	}
	if summaries[0].ObjectCount != 2 || summaries[0].TotalObjectBytes != 30 {
		t.Fatalf("photos summary = count %d bytes %d, want count 2 bytes 30", summaries[0].ObjectCount, summaries[0].TotalObjectBytes)
	}
	if summaries[1].Name != "docs" {
		t.Fatalf("second bucket = %q, want docs", summaries[1].Name)
	}
	if summaries[1].ObjectCount != 1 || summaries[1].TotalObjectBytes != 30 {
		t.Fatalf("docs summary = count %d bytes %d, want count 1 bytes 30", summaries[1].ObjectCount, summaries[1].TotalObjectBytes)
	}
}

func seedManagementRepositoryData(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	userRepo := NewUserRepository(db)
	bucketRepo := NewBucketRepository(db)
	objectRepo := NewObjectRepository(db)
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)

	users := []*User{
		{
			ID:          "admin-user",
			DisplayName: "Admin",
			AccessKeyID: "fbsa_admin",
			SecretHash:  "hash1",
			Role:        "admin",
			IsActive:    true,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "member-user",
			DisplayName: "Member",
			AccessKeyID: "fbsa_member",
			SecretHash:  "hash2",
			Role:        "member",
			IsActive:    false,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}
	for _, user := range users {
		if err := userRepo.Create(ctx, user); err != nil {
			t.Fatalf("create user: %v", err)
		}
	}

	buckets := []*Bucket{
		{Name: "photos", OwnerID: "admin-user", CreatedAt: now},
		{Name: "docs", OwnerID: "member-user", CreatedAt: now.Add(time.Second)},
	}
	for _, bucket := range buckets {
		if err := bucketRepo.Create(ctx, bucket); err != nil {
			t.Fatalf("create bucket: %v", err)
		}
	}

	objects := []*Object{
		{ID: uuid.NewString(), BucketName: "photos", Key: "a.jpg", Size: 10, ETag: "etag-a", ContentType: "image/jpeg", StoragePath: "photos/a.jpg", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), BucketName: "photos", Key: "b.jpg", Size: 20, ETag: "etag-b", ContentType: "image/jpeg", StoragePath: "photos/b.jpg", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), BucketName: "docs", Key: "c.txt", Size: 30, ETag: "etag-c", ContentType: "text/plain", StoragePath: "docs/c.txt", CreatedAt: now, UpdatedAt: now},
	}
	for _, obj := range objects {
		if err := objectRepo.Create(ctx, obj); err != nil {
			t.Fatalf("create object: %v", err)
		}
	}
}
