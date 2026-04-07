package storage
import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
)
const defaultDataDir = "./data"
type DiskEngine interface {
	Write(ctx context.Context, bucketName, key string, r io.Reader) (storagePath string, size int64, err error)
	Read(ctx context.Context, storagePath string) (io.ReadCloser, error)
	Open(ctx context.Context, storagePath string) (*os.File, error)
	Delete(ctx context.Context, storagePath string) error
	Reconcile(ctx context.Context, knownObjects func(bucketName string) ([]string, error)) error
	StoragePath(bucketName, key string) string
}
type engine struct {
	dataDir string
	tmpDir  string
}
func New(dataDir string) (*engine, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absDataDir, 0o755); err != nil {
		return nil, err
	}
	tmpDir := filepath.Join(absDataDir, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, err
	}
	return &engine{
		dataDir: absDataDir,
		tmpDir:  tmpDir,
	}, nil
}