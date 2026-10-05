package metadata

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/i-got-this-faa/fbs/internal/iam"
)

func uniqueTestUser(displayName string) *User {
	u := newTestUser()
	u.DisplayName = displayName
	u.AccessKeyID = "ak_" + uuid.New().String()
	u.SigV4AccessKeyID = "fbsv4_" + uuid.New().String()
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
		ID:            uuid.New().String(),
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

func newGrantBatchTestRepo(t *testing.T) (GrantRepository, *User, *User) {
	t.Helper()

	db, err := Open(t.TempDir() + "/grants-batch.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	users := NewUserRepository(db)
	owner := uniqueTestUser("Owner")
	grantee := uniqueTestUser("Grantee")
	for _, user := range []*User{owner, grantee} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	if err := NewBucketRepository(db).Create(ctx, &Bucket{Name: "b", OwnerID: owner.ID, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	return NewGrantRepository(db), owner, grantee
}

func newBatchGrant(granteeID string, action iam.Action) Grant {
	now := time.Now().UTC()
	return Grant{
		ID: uuid.New().String(), BucketName: "b", GranteeUserID: granteeID,
		Action: action, IsActive: true, CreatedAt: now, UpdatedAt: now,
	}
}

func TestGrantCreateIdempotentBatch(t *testing.T) {
	t.Parallel()

	grants, _, grantee := newGrantBatchTestRepo(t)
	ctx := context.Background()
	existing := newBatchGrant(grantee.ID, iam.ActionListBucket)
	if err := grants.Create(ctx, &existing); err != nil {
		t.Fatalf("create existing: %v", err)
	}

	fresh := newBatchGrant(grantee.ID, iam.ActionGetObject)
	results, err := grants.CreateIdempotentBatch(ctx, []Grant{newBatchGrant(grantee.ID, iam.ActionListBucket), fresh})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if !results[0].Existed || results[0].Grant.ID != existing.ID {
		t.Fatalf("duplicate result = %+v, want existing grant %s", results[0], existing.ID)
	}
	if results[1].Existed || results[1].Grant.ID != fresh.ID {
		t.Fatalf("new result = %+v, want inserted grant %s", results[1], fresh.ID)
	}
	if _, err := grants.GetByID(ctx, fresh.ID); err != nil {
		t.Fatalf("inserted grant not stored: %v", err)
	}
}

// A failed batch must leave every grant, including pre-existing ones, as it was.
func TestGrantCreateIdempotentBatchIsAtomic(t *testing.T) {
	t.Parallel()

	grants, _, grantee := newGrantBatchTestRepo(t)
	ctx := context.Background()
	existing := newBatchGrant(grantee.ID, iam.ActionGetObject)
	if err := grants.Create(ctx, &existing); err != nil {
		t.Fatalf("create existing: %v", err)
	}

	fresh := newBatchGrant(grantee.ID, iam.ActionListBucket)
	unknownGrantee := newBatchGrant("missing-user", iam.ActionPutObject)
	_, err := grants.CreateIdempotentBatch(ctx, []Grant{newBatchGrant(grantee.ID, iam.ActionGetObject), fresh, unknownGrantee})
	if err == nil {
		t.Fatal("batch with an unknown grantee must fail")
	}

	if _, err := grants.GetByID(ctx, existing.ID); err != nil {
		t.Fatalf("pre-existing grant was removed: %v", err)
	}
	if _, err := grants.GetByID(ctx, fresh.ID); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("grant from failed batch was committed: err = %v", err)
	}
}

func TestGrantCreateIdempotentBatchRejectsNonGrantableAction(t *testing.T) {
	t.Parallel()

	grants, _, grantee := newGrantBatchTestRepo(t)
	_, err := grants.CreateIdempotentBatch(context.Background(), []Grant{newBatchGrant(grantee.ID, iam.ActionDeleteBucket)})
	if !errors.Is(err, ErrInvalidGrantAction) {
		t.Fatalf("err = %v, want ErrInvalidGrantAction", err)
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
		ID: uuid.New().String(), BucketName: "b", GranteeUserID: grantee.ID,
		Action: "s3:DeleteBucket", IsActive: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrInvalidGrantAction) {
		t.Fatalf("err = %v, want ErrInvalidGrantAction", err)
	}
}

func TestGrantUpdateDuplicateActiveConflict(t *testing.T) {
	t.Parallel()

	db, err := Open(t.TempDir() + "/grants-dup.db")
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
	inactive := &Grant{
		ID: uuid.New().String(), BucketName: "b", GranteeUserID: grantee.ID,
		Action: "s3:GetObject", KeyPrefix: "docs/", IsActive: false,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := grants.Create(ctx, inactive); err != nil {
		t.Fatalf("create inactive: %v", err)
	}
	active := &Grant{
		ID: uuid.New().String(), BucketName: "b", GranteeUserID: grantee.ID,
		Action: "s3:GetObject", KeyPrefix: "docs/", IsActive: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := grants.Create(ctx, active); err != nil {
		t.Fatalf("create active: %v", err)
	}

	inactive.IsActive = true
	if err := grants.Update(ctx, inactive); !errors.Is(err, ErrDuplicateGrant) {
		t.Fatalf("update err = %v, want ErrDuplicateGrant", err)
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
	id := uuid.New().String()
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
