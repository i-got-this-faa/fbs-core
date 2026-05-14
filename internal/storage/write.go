package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

func (e *engine) Write(ctx context.Context, bucketName, key string, r io.Reader) (storagePath string, size int64, err error) {
	select {
	case <-ctx.Done():
		return "", 0, ctx.Err()
	default:
	}
	// Validate bucket/key but do not use the key-derived path. Write to a
	// unique UUID path so metadata commit is the commit point, and overwrites
	// never replace the old backing file before metadata is committed.
	if _, _, err := e.resolveKeyPath(bucketName, key); err != nil {
		return "", 0, err
	}
	storagePath = filepath.Join(bucketName, uuid.NewString())
	fullPath := filepath.Clean(filepath.Join(e.dataDir, storagePath))
	if !isWithinBase(e.dataDir, fullPath) {
		return "", 0, ErrPathTraversal
	}

	tempName := uuid.NewString() + ".tmp"
	tempPath := filepath.Join(e.tmpDir, tempName)
	tempFile, err := os.Create(tempPath)
	if err != nil {
		return "", 0, err
	}
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = os.Remove(tempPath)
		}
	}()
	written, err := copyWithContext(ctx, tempFile, r)
	if err != nil {
		_ = tempFile.Close()
		return "", 0, err
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return "", 0, err
	}
	if err := tempFile.Close(); err != nil {
		return "", 0, err
	}
	finalDir := filepath.Dir(fullPath)
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("create directories for key: %w", err)
	}
	if err := os.Rename(tempPath, fullPath); err != nil {
		return "", 0, err
	}
	cleanupTemp = false
	return storagePath, written, nil
}
func (e *engine) WritePart(ctx context.Context, uploadID string, partNumber int, r io.Reader) (storagePath string, size int64, err error) {
	select {
	case <-ctx.Done():
		return "", 0, ctx.Err()
	default:
	}

	if err := validateUploadID(uploadID); err != nil {
		return "", 0, err
	}

	partDir := filepath.Join(e.tmpDir, "multipart", uploadID)
	if err := os.MkdirAll(partDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("create multipart part directory: %w", err)
	}

	partName := fmt.Sprintf("%d", partNumber)
	tempName := partName + ".tmp-" + uuid.NewString()
	tempPath := filepath.Join(partDir, tempName)

	tempFile, err := os.Create(tempPath)
	if err != nil {
		return "", 0, err
	}

	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = os.Remove(tempPath)
		}
	}()

	written, err := copyWithContext(ctx, tempFile, r)
	if err != nil {
		_ = tempFile.Close()
		return "", 0, err
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return "", 0, err
	}
	if err := tempFile.Close(); err != nil {
		return "", 0, err
	}

	// Keep the unique filename as the final path so concurrent uploads of the
	// same part number cannot race on a fixed destination, and so a failed
	// re-upload does not overwrite a previously valid part before metadata is
	// committed. Old parts are cleaned up when the upload is completed or aborted.
	cleanupTemp = false
	storagePath = filepath.Join(".tmp", "multipart", uploadID, tempName)
	return storagePath, written, nil
}

func (e *engine) AssembleParts(ctx context.Context, bucketName, key string, partPaths []string) (storagePath string, size int64, err error) {
	select {
	case <-ctx.Done():
		return "", 0, ctx.Err()
	default:
	}

	if err := ValidateKey(key); err != nil {
		return "", 0, err
	}

	// Write the assembled object to a unique path under the bucket directory
	// so an existing object file is not overwritten before metadata commit.
	// Using a UUID filename (independent of the key) avoids exceeding filesystem
	// name limits and prevents races. Metadata is the commit point.
	objName := uuid.NewString()
	storagePath = filepath.Join(bucketName, objName)
	fullPath := filepath.Join(e.dataDir, storagePath)
	if !isWithinBase(e.dataDir, fullPath) {
		return "", 0, ErrPathTraversal
	}

	tempName := uuid.NewString() + ".tmp"
	tempPath := filepath.Join(e.tmpDir, tempName)
	tempFile, err := os.Create(tempPath)
	if err != nil {
		return "", 0, err
	}

	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = os.Remove(tempPath)
		}
	}()

	var totalSize int64
	for _, partPath := range partPaths {
		select {
		case <-ctx.Done():
			_ = tempFile.Close()
			return "", 0, ctx.Err()
		default:
		}

		partFullPath, resolveErr := e.resolveStoragePath(partPath)
		if resolveErr != nil {
			_ = tempFile.Close()
			return "", 0, fmt.Errorf("resolve part path %q: %w", partPath, resolveErr)
		}

		partFile, openErr := os.Open(partFullPath)
		if openErr != nil {
			_ = tempFile.Close()
			return "", 0, fmt.Errorf("open part %q: %w", partPath, openErr)
		}

		written, copyErr := copyWithContext(ctx, tempFile, partFile)
		_ = partFile.Close()
		if copyErr != nil {
			_ = tempFile.Close()
			return "", 0, fmt.Errorf("copy part %q: %w", partPath, copyErr)
		}
		totalSize += written
	}

	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return "", 0, err
	}
	if err := tempFile.Close(); err != nil {
		return "", 0, err
	}

	finalDir := filepath.Dir(fullPath)
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("create directories for key: %w", err)
	}
	if err := os.Rename(tempPath, fullPath); err != nil {
		return "", 0, err
	}

	cleanupTemp = false
	return storagePath, totalSize, nil
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 32*1024)
	var written int64
	for {
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		default:
		}
		nr, readErr := src.Read(buffer)
		if nr > 0 {
			nw, writeErr := dst.Write(buffer[:nr])
			written += int64(nw)
			if writeErr != nil {
				return written, writeErr
			}
			if nw != nr {
				return written, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return written, nil
			}
			return written, readErr
		}
	}
}
