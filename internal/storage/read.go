package storage
import (
	"context"
	"errors"
	"io"
	"os"
)
func (e *engine) Read(ctx context.Context, storagePath string) (io.ReadCloser, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	fullPath, err := e.resolveStoragePath(storagePath)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return file, nil
}
func (e *engine) Open(ctx context.Context, storagePath string) (*os.File, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	fullPath, err := e.resolveStoragePath(storagePath)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return file, nil
}