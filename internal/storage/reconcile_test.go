package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcilePurgesTmp(t *testing.T) {
	t.Parallel()

	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tmpFile := filepath.Join(eng.tmpDir, "stale.tmp")
	if err := os.WriteFile(tmpFile, []byte("stale"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := eng.Reconcile(context.Background(), func(bucketName string) ([]string, error) {
		return nil, nil
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if _, err := os.Stat(tmpFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tmp file stat error = %v, want %v", err, os.ErrNotExist)
	}
}

func TestReconcileRemovesOrphansAndKeepsKnownFiles(t *testing.T) {
	t.Parallel()

	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	keptPath, _, err := eng.Write(context.Background(), "docs", "keep.txt", strings.NewReader("keep"))
	if err != nil {
		t.Fatalf("Write keep error = %v", err)
	}
	orphanPath, _, err := eng.Write(context.Background(), "docs", "nested/orphan.txt", strings.NewReader("orphan"))
	if err != nil {
		t.Fatalf("Write orphan error = %v", err)
	}

	if err := eng.Reconcile(context.Background(), func(bucketName string) ([]string, error) {
		if bucketName != "docs" {
			return nil, nil
		}
		return []string{keptPath}, nil
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	keptFullPath, err := eng.resolveStoragePath(keptPath)
	if err != nil {
		t.Fatalf("resolveStoragePath kept error = %v", err)
	}
	orphanFullPath, err := eng.resolveStoragePath(orphanPath)
	if err != nil {
		t.Fatalf("resolveStoragePath orphan error = %v", err)
	}

	if _, err := os.Stat(keptFullPath); err != nil {
		t.Fatalf("kept file stat error = %v", err)
	}
	if _, err := os.Stat(orphanFullPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan file stat error = %v, want %v", err, os.ErrNotExist)
	}
	if _, err := os.Stat(filepath.Dir(orphanFullPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan parent dir stat error = %v, want %v", err, os.ErrNotExist)
	}
}

func TestReconcileCanceledContext(t *testing.T) {
	t.Parallel()

	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = eng.Reconcile(ctx, func(bucketName string) ([]string, error) {
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconcile() error = %v, want %v", err, context.Canceled)
	}
}
