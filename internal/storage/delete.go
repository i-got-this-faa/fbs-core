package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (e *engine) Delete(ctx context.Context, storagePath string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	fullPath, err := e.resolveStoragePath(storagePath)
	if err != nil {
		return err
	}
	if err := os.Remove(fullPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	e.pruneEmptyParents(fullPath)
	return nil
}

func (e *engine) pruneEmptyParents(fullPath string) {
	rel, err := filepath.Rel(e.dataDir, fullPath)
	if err != nil {
		return
	}
	bucketName := firstPathElement(rel)
	if bucketName == "" || bucketName == "." {
		return
	}
	bucketRoot := filepath.Join(e.dataDir, bucketName)
	currentDir := filepath.Dir(fullPath)
	for {
		if currentDir == e.dataDir || currentDir == bucketRoot {
			return
		}
		if err := os.Remove(currentDir); err != nil {
			return
		}
		currentDir = filepath.Dir(currentDir)
	}
}
func (e *engine) DeleteUploadParts(ctx context.Context, uploadID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if err := validateUploadID(uploadID); err != nil {
		return err
	}

	partDir := filepath.Join(e.tmpDir, "multipart", uploadID)
	if err := os.RemoveAll(partDir); err != nil {
		return fmt.Errorf("remove upload parts directory: %w", err)
	}
	return nil
}

func firstPathElement(path string) string {
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == "" {
		return ""
	}
	parts := strings.Split(cleaned, string(filepath.Separator))
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
