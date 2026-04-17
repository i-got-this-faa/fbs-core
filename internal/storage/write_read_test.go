package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReadDeleteLifecycle(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	content := "hello storage"
	storagePath, size, err := eng.Write(context.Background(), "docs", "a/b.txt", strings.NewReader(content))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", size, len(content))
	}
	rc, err := eng.Read(context.Background(), storagePath)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != content {
		t.Fatalf("content = %q, want %q", string(data), content)
	}
	if err := eng.Delete(context.Background(), storagePath); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	_, err = eng.Read(context.Background(), storagePath)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Read() after delete error = %v, want %v", err, ErrNotFound)
	}
}
func TestWriteCreatesIntermediateDirectories(t *testing.T) {
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
	if _, err := os.Stat(fullPath); err != nil {
		t.Fatalf("stored file stat error = %v", err)
	}
	parentDir := filepath.Dir(fullPath)
	if _, err := os.Stat(parentDir); err != nil {
		t.Fatalf("parent dir stat error = %v", err)
	}
}
func TestWriteCanceledContext(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = eng.Write(ctx, "docs", "file.txt", strings.NewReader("hello"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Write() error = %v, want %v", err, context.Canceled)
	}
}

func TestAtomicWriteFailureCleansTempFile(t *testing.T) {
	t.Parallel()

	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, _, err = eng.Write(context.Background(), "docs", "broken.txt", &failingReader{failAfter: 1})
	if err == nil {
		t.Fatal("Write() error = nil, want failure")
	}

	entries, err := os.ReadDir(eng.tmpDir)
	if err != nil {
		t.Fatalf("ReadDir(tmpDir) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("tmpDir entries = %d, want 0", len(entries))
	}

	_, statErr := os.Stat(filepath.Join(eng.dataDir, "docs", "broken.txt"))
	if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("final file stat error = %v, want %v", statErr, os.ErrNotExist)
	}
}

func TestReadNotFound(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = eng.Read(context.Background(), filepath.Join("docs", "missing.txt"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Read() error = %v, want %v", err, ErrNotFound)
	}
}
func TestOpenReturnsFile(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	content := "open me"
	storagePath, _, err := eng.Write(context.Background(), "docs", "file.txt", strings.NewReader(content))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	file, err := eng.Open(context.Background(), storagePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != content {
		t.Fatalf("content = %q, want %q", string(data), content)
	}
}
func TestOpenNotFound(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = eng.Open(context.Background(), filepath.Join("docs", "missing.txt"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open() error = %v, want %v", err, ErrNotFound)
	}
}

type failingReader struct {
	reads     int
	failAfter int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.reads >= r.failAfter {
		return 0, fmt.Errorf("forced read failure")
	}
	r.reads++
	copy(p, "x")
	return 1, nil
}
