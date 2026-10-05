package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"uuid"
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
	storagePath = filepath.Join(bucketName, uuid.New().String())
	fullPath := filepath.Clean(filepath.Join(e.dataDir, storagePath))
	if !isWithinBase(e.dataDir, fullPath) {
		return "", 0, ErrPathTraversal
	}

	tempPath := filepath.Join(e.tmpDir, uuid.New().String()+".tmp")
	written, err := writeDurably(tempPath, fullPath, func(w io.Writer) (int64, error) {
		return copyWithContext(ctx, w, r)
	})
	if err != nil {
		return "", 0, err
	}
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
	tempName := partName + ".tmp-" + uuid.New().String()
	// Keep the unique filename as the final path so concurrent uploads of the
	// same part number cannot race on a fixed destination, and so a failed
	// re-upload does not overwrite a previously valid part before metadata is
	// committed. Old parts are cleaned up when the upload is completed or aborted.
	partPath := filepath.Join(partDir, tempName)
	written, err := writeDurably(partPath, partPath, func(w io.Writer) (int64, error) {
		return copyWithContext(ctx, w, r)
	})
	if err != nil {
		return "", 0, err
	}
	return filepath.Join(".tmp", "multipart", uploadID, tempName), written, nil
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
	objName := uuid.New().String()
	storagePath = filepath.Join(bucketName, objName)
	fullPath := filepath.Join(e.dataDir, storagePath)
	if !isWithinBase(e.dataDir, fullPath) {
		return "", 0, ErrPathTraversal
	}

	tempPath := filepath.Join(e.tmpDir, uuid.New().String()+".tmp")
	totalSize, err := writeDurably(tempPath, fullPath, func(w io.Writer) (int64, error) {
		return e.copyParts(ctx, w, partPaths)
	})
	if err != nil {
		return "", 0, err
	}
	return storagePath, totalSize, nil
}

// copyParts appends each stored part to w in order and returns the byte count.
func (e *engine) copyParts(ctx context.Context, w io.Writer, partPaths []string) (int64, error) {
	var total int64
	for _, partPath := range partPaths {
		partFullPath, err := e.resolveStoragePath(partPath)
		if err != nil {
			return total, fmt.Errorf("resolve part path %q: %w", partPath, err)
		}
		partFile, err := os.Open(partFullPath)
		if err != nil {
			return total, fmt.Errorf("open part %q: %w", partPath, err)
		}
		written, err := copyWithContext(ctx, w, partFile)
		_ = partFile.Close()
		total += written
		if err != nil {
			return total, fmt.Errorf("copy part %q: %w", partPath, err)
		}
	}
	return total, nil
}

// writeDurably streams fill into a new file at tempPath, syncs it, and renames
// it to finalPath, then syncs finalPath's directory so the rename survives a
// crash. On any failure it removes tempPath and leaves finalPath untouched, so
// a reader never sees a partial file.
func writeDurably(tempPath, finalPath string, fill func(io.Writer) (int64, error)) (int64, error) {
	file, err := os.Create(tempPath)
	if err != nil {
		return 0, err
	}
	written, err := fill(file)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		if mkdirErr := os.MkdirAll(filepath.Dir(finalPath), 0o755); mkdirErr != nil {
			err = fmt.Errorf("create directories for key: %w", mkdirErr)
		}
	}
	if err == nil {
		err = os.Rename(tempPath, finalPath)
	}
	if err != nil {
		_ = os.Remove(tempPath)
		return 0, err
	}
	if err := syncDir(filepath.Dir(finalPath)); err != nil {
		_ = os.Remove(finalPath)
		return 0, fmt.Errorf("sync directory: %w", err)
	}
	return written, nil
}

// syncDir makes directory entry changes (create, rename) in dir durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	if closeErr := d.Close(); syncErr == nil {
		syncErr = closeErr
	}
	return syncErr
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
