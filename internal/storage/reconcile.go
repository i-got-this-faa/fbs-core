package storage

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
)

func (e *engine) Reconcile(ctx context.Context, knownObjects func(bucketName string) ([]string, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := e.purgeTmp(ctx); err != nil {
		return err
	}

	entries, err := os.ReadDir(e.dataDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() || entry.Name() == ".tmp" {
			continue
		}

		bucketName := entry.Name()
		known, err := knownObjects(bucketName)
		if err != nil {
			return err
		}

		knownSet := make(map[string]struct{}, len(known))
		for _, storagePath := range known {
			knownSet[normalizeKnownObjectPath(bucketName, storagePath)] = struct{}{}
		}

		bucketRoot := filepath.Join(e.dataDir, bucketName)
		if err := filepath.WalkDir(bucketRoot, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}

			relPath, err := filepath.Rel(e.dataDir, path)
			if err != nil {
				return err
			}
			relPath = filepath.Clean(relPath)
			if _, ok := knownSet[relPath]; ok {
				return nil
			}

			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}); err != nil {
			return err
		}

		e.pruneEmptyDirs(bucketRoot)
	}

	return nil
}

func (e *engine) purgeTmp(ctx context.Context) error {
	return filepath.WalkDir(e.tmpDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == e.tmpDir || d.IsDir() {
			return nil
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}

func normalizeKnownObjectPath(bucketName, storagePath string) string {
	cleaned := filepath.Clean(storagePath)
	if firstPathElement(cleaned) == bucketName {
		return cleaned
	}
	return filepath.Join(bucketName, cleaned)
}

func (e *engine) pruneEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})

	for i := len(dirs) - 1; i >= 0; i-- {
		dir := dirs[i]
		if dir == root {
			continue
		}
		_ = os.Remove(dir)
	}
}
