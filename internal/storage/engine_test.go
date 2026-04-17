package storage
import (
	"os"
	"path/filepath"
	"testing"
)
func TestNewCreatesDataAndTmpDirs(t *testing.T) {
	t.Parallel()
	baseDir := t.TempDir()
	dataDir := filepath.Join(baseDir, "data")
	eng, err := New(dataDir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if eng == nil {
		t.Fatal("New() returned nil engine")
	}
	if eng.dataDir == "" {
		t.Fatal("engine.dataDir is empty")
	}
	if eng.tmpDir == "" {
		t.Fatal("engine.tmpDir is empty")
	}
	if _, err := os.Stat(eng.dataDir); err != nil {
		t.Fatalf("dataDir stat error = %v", err)
	}
	if _, err := os.Stat(eng.tmpDir); err != nil {
		t.Fatalf("tmpDir stat error = %v", err)
	}
	wantTmpDir := filepath.Join(eng.dataDir, ".tmp")
	if eng.tmpDir != wantTmpDir {
		t.Fatalf("tmpDir = %q, want %q", eng.tmpDir, wantTmpDir)
	}
}