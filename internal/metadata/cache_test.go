package metadata

import (
	"context"
	"testing"
	"time"
)

func TestCachedObjectRepositoryGetByKeyCachesDelegateLookup(t *testing.T) {
	t.Parallel()

	delegate := newCountingObjectRepository()
	object := testCacheObject("bucket", "file.txt", "etag-1")
	delegate.objects[objectCacheMapKey(object.BucketName, object.Key)] = *object

	repo := NewCachedObjectRepository(delegate, NewMetadataCache(1024*1024))
	for range 2 {
		got, err := repo.GetByKey(context.Background(), object.BucketName, object.Key)
		if err != nil {
			t.Fatalf("GetByKey error = %v", err)
		}
		if got.ETag != object.ETag {
			t.Fatalf("ETag = %q, want %q", got.ETag, object.ETag)
		}
	}

	if delegate.getByKeyCalls != 1 {
		t.Fatalf("delegate GetByKey calls = %d, want 1", delegate.getByKeyCalls)
	}
}

func TestCachedObjectRepositoryReturnsCopies(t *testing.T) {
	t.Parallel()

	delegate := newCountingObjectRepository()
	object := testCacheObject("bucket", "file.txt", "etag-1")
	delegate.objects[objectCacheMapKey(object.BucketName, object.Key)] = *object

	repo := NewCachedObjectRepository(delegate, NewMetadataCache(1024*1024))
	got, err := repo.GetByKey(context.Background(), object.BucketName, object.Key)
	if err != nil {
		t.Fatalf("GetByKey error = %v", err)
	}
	got.ETag = "mutated"

	got, err = repo.GetByKey(context.Background(), object.BucketName, object.Key)
	if err != nil {
		t.Fatalf("GetByKey second error = %v", err)
	}
	if got.ETag != "etag-1" {
		t.Fatalf("cached ETag = %q, want original copy", got.ETag)
	}
}

func TestCachedObjectRepositoryCreateReplacesCachedObject(t *testing.T) {
	t.Parallel()

	delegate := newCountingObjectRepository()
	repo := NewCachedObjectRepository(delegate, NewMetadataCache(1024*1024))

	first := testCacheObject("bucket", "file.txt", "etag-1")
	if err := repo.Create(context.Background(), first); err != nil {
		t.Fatalf("create first: %v", err)
	}
	second := testCacheObject("bucket", "file.txt", "etag-2")
	if err := repo.Create(context.Background(), second); err != nil {
		t.Fatalf("create second: %v", err)
	}

	got, err := repo.GetByKey(context.Background(), "bucket", "file.txt")
	if err != nil {
		t.Fatalf("GetByKey error = %v", err)
	}
	if got.ETag != "etag-2" {
		t.Fatalf("ETag = %q, want replacement", got.ETag)
	}
}

func TestCachedObjectRepositoryDeleteEvictsCachedObject(t *testing.T) {
	t.Parallel()

	delegate := newCountingObjectRepository()
	object := testCacheObject("bucket", "file.txt", "etag-1")
	delegate.objects[objectCacheMapKey(object.BucketName, object.Key)] = *object

	repo := NewCachedObjectRepository(delegate, NewMetadataCache(1024*1024))
	if _, err := repo.GetByKey(context.Background(), "bucket", "file.txt"); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	if err := repo.Delete(context.Background(), "bucket", "file.txt"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByKey(context.Background(), "bucket", "file.txt"); err != ErrObjectNotFound {
		t.Fatalf("GetByKey after delete error = %v, want ErrObjectNotFound", err)
	}
}

func TestMetadataCacheBucketDeleteEvictsBucketAndObjects(t *testing.T) {
	t.Parallel()

	cache := NewMetadataCache(1024 * 1024)
	cache.putBucket(&Bucket{Name: "bucket", OwnerID: "owner", CreatedAt: time.Now().UTC()})
	cache.putObject(testCacheObject("bucket", "a.txt", "etag-a"))
	cache.putObject(testCacheObject("other", "b.txt", "etag-b"))

	cache.evictBucket("bucket")

	if _, ok := cache.getBucket("bucket"); ok {
		t.Fatal("bucket remained cached after eviction")
	}
	if _, ok := cache.getObject("bucket", "a.txt"); ok {
		t.Fatal("object under deleted bucket remained cached")
	}
	if _, ok := cache.getObject("other", "b.txt"); !ok {
		t.Fatal("object in other bucket should remain cached")
	}
}

func TestMetadataCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	t.Parallel()

	first := testCacheObject("bucket", "one.txt", "etag-1")
	second := testCacheObject("bucket", "two.txt", "etag-2")
	third := testCacheObject("bucket", "six.txt", "etag-3")
	cache := NewMetadataCache(estimateObjectSize(first) + estimateObjectSize(second))

	cache.putObject(first)
	cache.putObject(second)
	if _, ok := cache.getObject("bucket", "one.txt"); !ok {
		t.Fatal("expected first object cached")
	}
	cache.putObject(third)

	if _, ok := cache.getObject("bucket", "two.txt"); ok {
		t.Fatal("least recently used object remained cached")
	}
	if _, ok := cache.getObject("bucket", "one.txt"); !ok {
		t.Fatal("recently used object was evicted")
	}
	if _, ok := cache.getObject("bucket", "six.txt"); !ok {
		t.Fatal("new object was not cached")
	}
}

func TestMetadataCacheDisabledPassesThrough(t *testing.T) {
	t.Parallel()

	delegate := newCountingObjectRepository()
	object := testCacheObject("bucket", "file.txt", "etag-1")
	delegate.objects[objectCacheMapKey(object.BucketName, object.Key)] = *object

	repo := NewCachedObjectRepository(delegate, NewMetadataCache(0))
	for range 2 {
		if _, err := repo.GetByKey(context.Background(), "bucket", "file.txt"); err != nil {
			t.Fatalf("GetByKey error = %v", err)
		}
	}
	if delegate.getByKeyCalls != 2 {
		t.Fatalf("delegate GetByKey calls = %d, want 2", delegate.getByKeyCalls)
	}
}

func testCacheObject(bucketName, key, etag string) *Object {
	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	return &Object{
		ID:          bucketName + "-" + key,
		BucketName:  bucketName,
		Key:         key,
		Size:        10,
		ETag:        etag,
		ContentType: "text/plain",
		StoragePath: bucketName + "/" + key,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func objectCacheMapKey(bucketName, key string) string {
	return bucketName + "\x00" + key
}

type countingObjectRepository struct {
	objects       map[string]Object
	getByKeyCalls int
}

func newCountingObjectRepository() *countingObjectRepository {
	return &countingObjectRepository{objects: make(map[string]Object)}
}

func (r *countingObjectRepository) Create(_ context.Context, object *Object) error {
	r.objects[objectCacheMapKey(object.BucketName, object.Key)] = *cloneObject(object)
	return nil
}

func (r *countingObjectRepository) GetByKey(_ context.Context, bucketName, key string) (*Object, error) {
	r.getByKeyCalls++
	object, ok := r.objects[objectCacheMapKey(bucketName, key)]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return cloneObject(&object), nil
}

func (r *countingObjectRepository) List(_ context.Context, _ string, _ string, _ string, _ int) ([]Object, bool, error) {
	return nil, false, nil
}

func (r *countingObjectRepository) ListDelimited(_ context.Context, _ string, _ string, _ string, _ string, _ int) ([]DelimitedListEntry, error) {
	return nil, nil
}

func (r *countingObjectRepository) Delete(_ context.Context, bucketName, key string) error {
	mapKey := objectCacheMapKey(bucketName, key)
	if _, ok := r.objects[mapKey]; !ok {
		return ErrObjectNotFound
	}
	delete(r.objects, mapKey)
	return nil
}

func (r *countingObjectRepository) DeleteAllInBucket(_ context.Context, bucketName string) error {
	for key, object := range r.objects {
		if object.BucketName == bucketName {
			delete(r.objects, key)
		}
	}
	return nil
}
