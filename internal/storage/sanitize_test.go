package storage
import (
	"errors"
	"path/filepath"
	"testing"
)
func TestValidateKey_ValidKeys(t *testing.T) {
	t.Parallel()
	validKeys := []string{
		"file.txt",
		"a/b/c.txt",
		"backup-v2.tar.gz",
		"photos/2024/image.jpg",
	}
	for _, key := range validKeys {
		key := key
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			if err := ValidateKey(key); err != nil {
				t.Fatalf("ValidateKey(%q) error = %v, want nil", key, err)
			}
		})
	}
}
func TestValidateKey_RejectsInvalidKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  string
		want error
	}{
		{name: "empty", key: "", want: ErrInvalidKey},
		{name: "dot", key: ".", want: ErrInvalidKey},
		{name: "dotdot", key: "..", want: ErrInvalidKey},
		{name: "absolute", key: "/tmp/x", want: ErrInvalidKey},
		{name: "traversal", key: "../etc/passwd", want: ErrPathTraversal},
		{name: "nested traversal", key: "a/../../b", want: ErrPathTraversal},
		{name: "windows traversal", key: `..\secret.txt`, want: ErrPathTraversal},
		{name: "null byte", key: "bad\x00key", want: ErrInvalidKey},
		{name: "newline", key: "bad\nkey", want: ErrInvalidKey},
		{name: "carriage return", key: "bad\rkey", want: ErrInvalidKey},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateKey(tc.key)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ValidateKey(%q) error = %v, want %v", tc.key, err, tc.want)
			}
		})
	}
}
func TestStoragePath(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got := eng.StoragePath("docs", "a/../b/file.txt")
	want := filepath.Join("docs", "b", "file.txt")
	if got != want {
		t.Fatalf("StoragePath() = %q, want %q", got, want)
	}
}
func TestResolveKeyPath(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	storagePath, fullPath, err := eng.resolveKeyPath("docs", "a/b.txt")
	if err != nil {
		t.Fatalf("resolveKeyPath() error = %v", err)
	}
	wantStoragePath := filepath.Join("docs", "a", "b.txt")
	if storagePath != wantStoragePath {
		t.Fatalf("storagePath = %q, want %q", storagePath, wantStoragePath)
	}
	wantFullPath := filepath.Join(eng.dataDir, wantStoragePath)
	if fullPath != wantFullPath {
		t.Fatalf("fullPath = %q, want %q", fullPath, wantFullPath)
	}
}
func TestResolveKeyPath_RejectsEmptyBucket(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, _, err = eng.resolveKeyPath("", "file.txt")
	if !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("resolveKeyPath() error = %v, want %v", err, ErrInvalidKey)
	}
}
func TestResolveStoragePath_RejectsEscape(t *testing.T) {
	t.Parallel()
	eng, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = eng.resolveStoragePath(filepath.Join("..", "outside.txt"))
	if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("resolveStoragePath() error = %v, want %v", err, ErrPathTraversal)
	}
}
func TestIsWithinBase(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	targetInside := filepath.Join(base, "docs", "a.txt")
	targetOutside := filepath.Join(filepath.Dir(base), "outside.txt")
	if !isWithinBase(base, targetInside) {
		t.Fatalf("isWithinBase() = false, want true for inside path")
	}
	if isWithinBase(base, targetOutside) {
		t.Fatalf("isWithinBase() = true, want false for outside path")
	}
}