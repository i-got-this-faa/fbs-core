package metadata

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

type shareLinkTestEnv struct {
	links   ShareLinkRepository
	buckets BucketRepository
	userID  string
}

func newShareLinkTestEnv(t *testing.T) shareLinkTestEnv {
	t.Helper()

	db, err := Open(t.TempDir() + "/share-links.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	users := NewUserRepository(db)
	owner := uniqueTestUser("Owner")
	if err := users.Create(ctx, owner); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	buckets := NewBucketRepository(db)
	for _, name := range []string{"photos", "videos"} {
		if err := buckets.Create(ctx, &Bucket{Name: name, OwnerID: owner.ID, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatalf("create bucket %s: %v", name, err)
		}
	}

	return shareLinkTestEnv{links: NewShareLinkRepository(db), buckets: buckets, userID: owner.ID}
}

func (e shareLinkTestEnv) mustCreate(t *testing.T, code, bucket, key string, expiresAt *time.Time, createdAt time.Time) {
	t.Helper()
	err := e.links.Create(context.Background(), &ShareLink{
		Code:       code,
		BucketName: bucket,
		ObjectKey:  key,
		CreatedBy:  e.userID,
		ExpiresAt:  expiresAt,
		CreatedAt:  createdAt,
	})
	if err != nil {
		t.Fatalf("create share link %s: %v", code, err)
	}
}

func TestShareLinkCreateGetRoundTrip(t *testing.T) {
	t.Parallel()

	env := newShareLinkTestEnv(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Hour)

	err := env.links.Create(ctx, &ShareLink{
		Code:                       "clip",
		BucketName:                 "videos",
		ObjectKey:                  "2026/clip.mp4",
		ResponseContentDisposition: `inline; filename="clip.mp4"`,
		CreatedBy:                  env.userID,
		ExpiresAt:                  &expiresAt,
		CreatedAt:                  now,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := env.links.GetByCode(ctx, "clip")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.BucketName != "videos" || got.ObjectKey != "2026/clip.mp4" || got.CreatedBy != env.userID {
		t.Fatalf("unexpected link: %+v", got)
	}
	if got.ResponseContentDisposition != `inline; filename="clip.mp4"` {
		t.Fatalf("disposition = %q", got.ResponseContentDisposition)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, expiresAt)
	}
	if !got.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, now)
	}
}

func TestShareLinkWithoutExpiry(t *testing.T) {
	t.Parallel()

	env := newShareLinkTestEnv(t)
	env.mustCreate(t, "forever", "photos", "a.jpg", nil, time.Now().UTC())

	got, err := env.links.GetByCode(context.Background(), "forever")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ExpiresAt != nil {
		t.Fatalf("ExpiresAt = %v, want nil", got.ExpiresAt)
	}
	if got.IsExpired(time.Now().Add(1000 * time.Hour)) {
		t.Fatal("link without expiry must never expire")
	}
}

func TestShareLinkIsExpired(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	link := ShareLink{ExpiresAt: &expiresAt}
	if link.IsExpired(expiresAt.Add(-time.Second)) {
		t.Fatal("link expired before its expiry")
	}
	if !link.IsExpired(expiresAt) {
		t.Fatal("link must be expired at its expiry")
	}
}

func TestShareLinkDuplicateCode(t *testing.T) {
	t.Parallel()

	env := newShareLinkTestEnv(t)
	env.mustCreate(t, "taken", "photos", "a.jpg", nil, time.Now().UTC())

	err := env.links.Create(context.Background(), &ShareLink{
		Code: "taken", BucketName: "photos", ObjectKey: "b.jpg", CreatedBy: env.userID, CreatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrShareLinkCodeTaken) {
		t.Fatalf("err = %v, want ErrShareLinkCodeTaken", err)
	}
}

func TestShareLinkGetMissing(t *testing.T) {
	t.Parallel()

	env := newShareLinkTestEnv(t)
	if _, err := env.links.GetByCode(context.Background(), "missing"); !errors.Is(err, ErrShareLinkNotFound) {
		t.Fatalf("err = %v, want ErrShareLinkNotFound", err)
	}
}

func TestShareLinkListFiltersAndOrders(t *testing.T) {
	t.Parallel()

	env := newShareLinkTestEnv(t)
	base := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	env.mustCreate(t, "old", "photos", "a.jpg", nil, base)
	env.mustCreate(t, "new", "photos", "b.jpg", nil, base.Add(time.Minute))
	env.mustCreate(t, "vid", "videos", "c.mp4", nil, base.Add(2*time.Minute))

	all, err := env.links.List(context.Background(), ShareLinkListFilter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if got := shareLinkCodes(all); !slices.Equal(got, []string{"vid", "new", "old"}) {
		t.Fatalf("all codes = %v", got)
	}

	photos, err := env.links.List(context.Background(), ShareLinkListFilter{BucketName: "photos"})
	if err != nil {
		t.Fatalf("list photos: %v", err)
	}
	if got := shareLinkCodes(photos); !slices.Equal(got, []string{"new", "old"}) {
		t.Fatalf("photos codes = %v", got)
	}
}

func TestShareLinkDelete(t *testing.T) {
	t.Parallel()

	env := newShareLinkTestEnv(t)
	ctx := context.Background()
	env.mustCreate(t, "gone", "photos", "a.jpg", nil, time.Now().UTC())

	if err := env.links.Delete(ctx, "gone"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := env.links.Delete(ctx, "gone"); !errors.Is(err, ErrShareLinkNotFound) {
		t.Fatalf("second delete err = %v, want ErrShareLinkNotFound", err)
	}
}

func TestShareLinkCascadeOnBucketDelete(t *testing.T) {
	t.Parallel()

	env := newShareLinkTestEnv(t)
	ctx := context.Background()
	env.mustCreate(t, "photo", "photos", "a.jpg", nil, time.Now().UTC())

	if err := env.buckets.Delete(ctx, "photos"); err != nil {
		t.Fatalf("delete bucket must not be blocked by share links: %v", err)
	}
	if _, err := env.links.GetByCode(ctx, "photo"); !errors.Is(err, ErrShareLinkNotFound) {
		t.Fatalf("after bucket delete err = %v, want ErrShareLinkNotFound", err)
	}
}

func shareLinkCodes(links []ShareLink) []string {
	codes := make([]string, 0, len(links))
	for _, link := range links {
		codes = append(codes, link.Code)
	}
	return codes
}
