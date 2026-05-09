package objectops

import (
	"context"
	"errors"

	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

const pageLimit = 1000

func ListAllObjects(ctx context.Context, repo metadata.ObjectRepository, bucketName string) ([]metadata.Object, error) {
	var allObjects []metadata.Object
	startAfter := ""
	for {
		objects, isTruncated, err := repo.List(ctx, bucketName, "", startAfter, pageLimit)
		if err != nil {
			return nil, err
		}
		allObjects = append(allObjects, objects...)
		if !isTruncated || len(objects) == 0 {
			return allObjects, nil
		}
		startAfter = objects[len(objects)-1].Key
	}
}

func DeleteObject(ctx context.Context, repo metadata.ObjectRepository, disk storage.DiskEngine, bucketName, key string) (*metadata.Object, bool, error) {
	obj, err := repo.GetByKey(ctx, bucketName, key)
	if errors.Is(err, metadata.ErrObjectNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	if err := repo.Delete(ctx, bucketName, key); err != nil {
		return nil, false, err
	}

	if disk != nil {
		if err := disk.Delete(ctx, obj.StoragePath); err != nil {
			return nil, false, err
		}
	}

	return obj, true, nil
}

func EmptyBucket(ctx context.Context, repo metadata.ObjectRepository, disk storage.DiskEngine, bucketName string) ([]metadata.Object, error) {
	objects, err := ListAllObjects(ctx, repo, bucketName)
	if err != nil {
		return nil, err
	}

	if disk != nil {
		for _, obj := range objects {
			if err := disk.Delete(ctx, obj.StoragePath); err != nil {
				return nil, err
			}
		}
	}

	if err := repo.DeleteAllInBucket(ctx, bucketName); err != nil {
		return nil, err
	}

	return objects, nil
}
