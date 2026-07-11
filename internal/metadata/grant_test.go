package metadata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func uniqueTestUser(displayName string) *User {
	u := newTestUser()
	u.DisplayName = displayName
	u.AccessKeyID = "ak_" + uuid.NewString()
	u.SigV4AccessKeyID = "fbsv4_" + uuid.NewString()
	return u
}

func TestGrantCreateAndList(t *testing.T) {
	t.Parallel()

	db, err := Open(t.TempDir() + "/grants.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	users := NewUserRepository(db)
	owner := uniqueTestUser("Owner")
	grantee := uniqueTestUser("Grantee")
	if err := users.Create(ctx, owner); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if err := users.Create(ctx, grantee); err != nil {
		t.Fatalf("create grantee: %v", err)
	}

	buckets := NewBucketRepository(db)
	if err := buckets.Create(ctx, &Bucket{Name: "photos", OwnerID: owner.ID, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	grants := NewGrantRepository(db)
	now := time.Now().UTC().Truncate(time.Second)
	g := &Grant{
		ID:            uuid.NewString(),
		BucketName:    "photos",
		GranteeUserID: grantee.ID,
		Action:        "s3:GetObject",
		KeyPrefix:     "2026/",
		IsActive:      true,
		CreatedBy:     owner.ID,
		Note:          "read 2026",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := grants.Create(ctx, g); err != nil {
		t.Fatalf("create grant: %v", err)
	}

	listed, err := grants.ListByBucket(ctx, "photos")
	if err != nil {
		t.Fatalf("list by bucket: %v", err)
	}
	if len(listed) != 1 || listed[0].Action != "s3:GetObject" {
		t.Fatalf("listed = %+v", listed)
	}

	active, err := grants.ListActiveForGranteeBucket(ctx, grantee.ID, "photos")
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 1 || active[0].KeyPrefix != "2026/" {
		t.Fatalf("active = %+v", active)
	}

	names, err := grants.ListBucketNamesWithActiveGrants(ctx, grantee.ID)
	if err != nil {
		t.Fatalf("bucket names: %v", err)
	}
	if len(names) != 1 || names[0] != "photos" {
		t.Fatalf("names = %v", names)
	}
}

func TestGrantCreateIdempotent(t *testing.T) {
	t.Parallel()

	db, err := Open(t.TempDir() + "/grants-idem.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	users := NewUserRepository(db)
	owner := uniqueTestUser("Owner")
	grantee := uniqueTestUser("Grantee")
	if err := users.Create(ctx, owner); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if err := users.Create(ctx, grantee); err != nil {
		t.Fatalf("create grantee: %v", err)
	}
	if err := NewBucketRepository(db).Create(ctx, &Bucket{Name: "b", OwnerID: owner.ID, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	grants := NewGrantRepository(db)
	now := time.Now().UTC()
	first := &Grant{
		ID: uuid.NewString(), BucketName: "b", GranteeUserID: grantee.ID,
		Action: "s3:ListBucket", IsActive: true, CreatedBy: owner.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	got, existed, err := grants.CreateIdempotent(ctx, first)
	if err != nil || existed {
		t.Fatalf("first: got=%+v existed=%v err=%v", got, existed, err)
	}

	second := &Grant{
		ID: uuid.NewString(), BucketName: "b", GranteeUserID: grantee.ID,
		Action: "s3:ListBucket", IsActive: true, CreatedBy: owner.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	got, existed, err = grants.CreateIdempotent(ctx, second)
	if err != nil || !existed {
		t.Fatalf("second: got=%+v existed=%v err=%v", got, existed, err)
	}
	if got.ID != first.ID {
		t.Fatalf("id = %s, want %s", got.ID, first.ID)
	}
}

func TestGrantRejectsNonGrantableAction(t *testing.T) {
	t.Parallel()

	db, err := Open(t.TempDir() + "/grants-bad.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	users := NewUserRepository(db)
	owner := uniqueTestUser("Owner")
	grantee := uniqueTestUser("Grantee")
	if err := users.Create(ctx, owner); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if err := users.Create(ctx, grantee); err != nil {
		t.Fatalf("create grantee: %v", err)
	}
	if err := NewBucketRepository(db).Create(ctx, &Bucket{Name: "b", OwnerID: owner.ID, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	err = NewGrantRepository(db).Create(ctx, &Grant{
		ID: uuid.NewString(), BucketName: "b", GranteeUserID: grantee.ID,
		Action: "s3:DeleteBucket", IsActive: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrInvalidGrantAction) {
		t.Fatalf("err = %v, want ErrInvalidGrantAction", err)
	}
}

func TestGrantCascadeOnBucketDelete(t *testing.T) {
	t.Parallel()

	db, err := Open(t.TempDir() + "/grants-cascade.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	users := NewUserRepository(db)
	owner := uniqueTestUser("Owner")
	grantee := uniqueTestUser("Grantee")
	if err := users.Create(ctx, owner); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if err := users.Create(ctx, grantee); err != nil {
		t.Fatalf("create grantee: %v", err)
	}
	buckets := NewBucketRepository(db)
	if err := buckets.Create(ctx, &Bucket{Name: "gone", OwnerID: owner.ID, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	grants := NewGrantRepository(db)
	id := uuid.NewString()
	if err := grants.Create(ctx, &Grant{
		ID: id, BucketName: "gone", GranteeUserID: grantee.ID,
		Action: "s3:GetObject", IsActive: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create grant: %v", err)
	}

	if err := buckets.Delete(ctx, "gone"); err != nil {
		t.Fatalf("delete bucket: %v", err)
	}
	if _, err := grants.GetByID(ctx, id); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("grant after cascade: %v", err)
	}
}
