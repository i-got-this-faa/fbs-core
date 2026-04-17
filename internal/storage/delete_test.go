package storage
import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)
func TestDeleteExisting(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	storagePath, _, err := eng.Write(context.Background(), "docs", "file.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	fullPath, err := eng.resolveStoragePath(storagePath)
	if err != nil {
		t.Fatalf("resolveStoragePath() error = %v", err)
	}
	if err := eng.Delete(context.Background(), storagePath); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(fullPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat error = %v, want %v", err, os.ErrNotExist)
	}
}
func TestDeleteIdempotent(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	storagePath, _, err := eng.Write(context.Background(), "docs", "file.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := eng.Delete(context.Background(), storagePath); err != nil {
		t.Fatalf("first Delete() error = %v", err)
	}
	if err := eng.Delete(context.Background(), storagePath); err != nil {
		t.Fatalf("second Delete() error = %v, want nil", err)
	}
}
func TestDeletePrunesEmptyParents(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	storagePath, _, err := eng.Write(context.Background(), "docs", "a/b/c/file.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	fullPath, err := eng.resolveStoragePath(storagePath)
	if err != nil {
		t.Fatalf("resolveStoragePath() error = %v", err)
	}
	bucketRoot := filepath.Join(eng.dataDir, "docs")
	dirA := filepath.Join(bucketRoot, "a")
	dirB := filepath.Join(dirA, "b")
	dirC := filepath.Join(dirB, "c")
	if err := eng.Delete(context.Background(), storagePath); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(fullPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file stat error = %v, want %v", err, os.ErrNotExist)
	}
	if _, err := os.Stat(dirC); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dirC stat error = %v, want %v", err, os.ErrNotExist)
	}
	if _, err := os.Stat(dirB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dirB stat error = %v, want %v", err, os.ErrNotExist)
	}
	if _, err := os.Stat(dirA); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dirA stat error = %v, want %v", err, os.ErrNotExist)
	}
	if _, err := os.Stat(bucketRoot); err != nil {
		t.Fatalf("bucketRoot stat error = %v, want bucket root to remain", err)
	}
}
func TestDeleteCanceledContext(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	storagePath, _, err := eng.Write(context.Background(), "docs", "file.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = eng.Delete(ctx, storagePath)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete() error = %v, want %v", err, context.Canceled)
	}
}