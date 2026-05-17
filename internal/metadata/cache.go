package metadata

import (
	"container/list"
	"context"
	"sync"
)

type cacheKind uint8

const (
	cacheKindBucket cacheKind = iota + 1
	cacheKindObject

	bucketCacheOverhead = int64(128)
	objectCacheOverhead = int64(256)
)

type cacheKey struct {
	kind       cacheKind
	bucketName string
	objectKey  string
}

type cacheValue struct {
	bucket *Bucket
	object *Object
}

type cacheEntry struct {
	key   cacheKey
	value cacheValue
	size  int64
}

type MetadataCache struct {
	mu       sync.Mutex
	maxBytes int64
	used     int64
	lru      *list.List
	entries  map[cacheKey]*list.Element
}

func NewMetadataCache(maxBytes int64) *MetadataCache {
	return &MetadataCache{
		maxBytes: maxBytes,
		lru:      list.New(),
		entries:  make(map[cacheKey]*list.Element),
	}
}

func (c *MetadataCache) getBucket(name string) (*Bucket, bool) {
	if c == nil || c.maxBytes <= 0 {
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.entries[bucketKey(name)]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(element)

	entry := element.Value.(*cacheEntry)
	return cloneBucket(entry.value.bucket), true
}

func (c *MetadataCache) putBucket(bucket *Bucket) {
	if c == nil || c.maxBytes <= 0 || bucket == nil {
		return
	}

	c.put(cacheEntry{
		key:   bucketKey(bucket.Name),
		value: cacheValue{bucket: cloneBucket(bucket)},
		size:  estimateBucketSize(bucket),
	})
}

func (c *MetadataCache) getObject(bucketName, key string) (*Object, bool) {
	if c == nil || c.maxBytes <= 0 {
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.entries[objectKey(bucketName, key)]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(element)

	entry := element.Value.(*cacheEntry)
	return cloneObject(entry.value.object), true
}

func (c *MetadataCache) putObject(object *Object) {
	if c == nil || c.maxBytes <= 0 || object == nil {
		return
	}

	c.put(cacheEntry{
		key:   objectKey(object.BucketName, object.Key),
		value: cacheValue{object: cloneObject(object)},
		size:  estimateObjectSize(object),
	})
}

func (c *MetadataCache) evictBucket(name string) {
	if c == nil || c.maxBytes <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.removeLocked(bucketKey(name))
	for key := range c.entries {
		if key.kind == cacheKindObject && key.bucketName == name {
			c.removeLocked(key)
		}
	}
}

func (c *MetadataCache) evictObject(bucketName, key string) {
	if c == nil || c.maxBytes <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(objectKey(bucketName, key))
}

func (c *MetadataCache) evictObjectsInBucket(bucketName string) {
	if c == nil || c.maxBytes <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.entries {
		if key.kind == cacheKindObject && key.bucketName == bucketName {
			c.removeLocked(key)
		}
	}
}

func (c *MetadataCache) put(entry cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.entries[entry.key]; ok {
		c.removeElementLocked(existing)
	}

	element := c.lru.PushFront(&entry)
	c.entries[entry.key] = element
	c.used += entry.size
	c.evictOverBudgetLocked()
}

func (c *MetadataCache) evictOverBudgetLocked() {
	for c.used > c.maxBytes {
		element := c.lru.Back()
		if element == nil {
			return
		}
		c.removeElementLocked(element)
	}
}

func (c *MetadataCache) removeLocked(key cacheKey) {
	element, ok := c.entries[key]
	if !ok {
		return
	}
	c.removeElementLocked(element)
}

func (c *MetadataCache) removeElementLocked(element *list.Element) {
	entry := element.Value.(*cacheEntry)
	delete(c.entries, entry.key)
	c.used -= entry.size
	c.lru.Remove(element)
}

func bucketKey(name string) cacheKey {
	return cacheKey{kind: cacheKindBucket, bucketName: name}
}

func objectKey(bucketName, key string) cacheKey {
	return cacheKey{kind: cacheKindObject, bucketName: bucketName, objectKey: key}
}

func estimateBucketSize(bucket *Bucket) int64 {
	return bucketCacheOverhead + int64(len(bucket.Name)+len(bucket.OwnerID))
}

func estimateObjectSize(object *Object) int64 {
	return objectCacheOverhead + int64(len(object.ID)+len(object.BucketName)+len(object.Key)+len(object.ETag)+len(object.ContentType)+len(object.StoragePath))
}

func cloneBucket(bucket *Bucket) *Bucket {
	if bucket == nil {
		return nil
	}
	copied := *bucket
	return &copied
}

func cloneObject(object *Object) *Object {
	if object == nil {
		return nil
	}
	copied := *object
	return &copied
}

type cachedBucketRepository struct {
	delegate BucketRepository
	cache    *MetadataCache
}

func NewCachedBucketRepository(delegate BucketRepository, cache *MetadataCache) BucketRepository {
	return &cachedBucketRepository{delegate: delegate, cache: cache}
}

func (r *cachedBucketRepository) Create(ctx context.Context, bucket *Bucket) error {
	if err := r.delegate.Create(ctx, bucket); err != nil {
		return err
	}
	r.cache.putBucket(bucket)
	return nil
}

func (r *cachedBucketRepository) GetByName(ctx context.Context, name string) (*Bucket, error) {
	if bucket, ok := r.cache.getBucket(name); ok {
		return bucket, nil
	}

	bucket, err := r.delegate.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	r.cache.putBucket(bucket)
	return cloneBucket(bucket), nil
}

func (r *cachedBucketRepository) List(ctx context.Context) ([]Bucket, error) {
	return r.delegate.List(ctx)
}

func (r *cachedBucketRepository) ListByOwner(ctx context.Context, ownerID string) ([]Bucket, error) {
	return r.delegate.ListByOwner(ctx, ownerID)
}

func (r *cachedBucketRepository) Delete(ctx context.Context, name string) error {
	if err := r.delegate.Delete(ctx, name); err != nil {
		return err
	}
	r.cache.evictBucket(name)
	return nil
}

type cachedObjectRepository struct {
	delegate ObjectRepository
	cache    *MetadataCache
}

func NewCachedObjectRepository(delegate ObjectRepository, cache *MetadataCache) ObjectRepository {
	return &cachedObjectRepository{delegate: delegate, cache: cache}
}

func PutObjectInCache(repo ObjectRepository, object *Object) {
	cached, ok := repo.(*cachedObjectRepository)
	if !ok {
		return
	}
	cached.cache.putObject(object)
}

func (r *cachedObjectRepository) Create(ctx context.Context, object *Object) error {
	if err := r.delegate.Create(ctx, object); err != nil {
		return err
	}
	r.cache.putObject(object)
	return nil
}

func (r *cachedObjectRepository) GetByKey(ctx context.Context, bucketName, key string) (*Object, error) {
	if object, ok := r.cache.getObject(bucketName, key); ok {
		return object, nil
	}

	object, err := r.delegate.GetByKey(ctx, bucketName, key)
	if err != nil {
		return nil, err
	}
	r.cache.putObject(object)
	return cloneObject(object), nil
}

func (r *cachedObjectRepository) List(ctx context.Context, bucketName, prefix, startAfter string, maxKeys int) ([]Object, bool, error) {
	return r.delegate.List(ctx, bucketName, prefix, startAfter, maxKeys)
}

func (r *cachedObjectRepository) ListDelimited(ctx context.Context, bucketName, prefix, startAfter, delimiter string, maxKeys int) ([]DelimitedListEntry, error) {
	return r.delegate.ListDelimited(ctx, bucketName, prefix, startAfter, delimiter, maxKeys)
}

func (r *cachedObjectRepository) Delete(ctx context.Context, bucketName, key string) error {
	if err := r.delegate.Delete(ctx, bucketName, key); err != nil {
		return err
	}
	r.cache.evictObject(bucketName, key)
	return nil
}

func (r *cachedObjectRepository) DeleteAllInBucket(ctx context.Context, bucketName string) error {
	if err := r.delegate.DeleteAllInBucket(ctx, bucketName); err != nil {
		return err
	}
	r.cache.evictObjectsInBucket(bucketName)
	return nil
}
