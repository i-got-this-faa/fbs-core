package storage

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func ValidateKey(key string) error {
	if key == "" {
		return ErrInvalidKey
	}

	if len(key) > 1024 {
		return ErrInvalidKey
	}

	if key == "." || key == ".." {
		return ErrInvalidKey
	}

	if strings.HasPrefix(key, "/") {
		return ErrInvalidKey
	}

	if strings.Contains(key, "\x00") {
		return ErrInvalidKey
	}

	if strings.Contains(key, "\n") || strings.Contains(key, "\r") {
		return ErrInvalidKey
	}

	if containsTraversalSegment(key) {
		return ErrPathTraversal
	}

	if !utf8.ValidString(key) {
		return ErrInvalidKey
	}

	cleaned := filepath.Clean(key)
	if cleaned == "." || cleaned == "" {
		return ErrInvalidKey
	}

	return nil
}

func (e *engine) StoragePath(bucketName, key string) string {
	return filepath.Join(bucketName, filepath.Clean(key))
}

func (e *engine) resolveKeyPath(bucketName, key string) (storagePath string, fullPath string, err error) {
	bucketName = strings.TrimSpace(bucketName)

	if bucketName == "" {
		return "", "", ErrInvalidKey
	}

	if err := ValidateKey(key); err != nil {
		return "", "", err
	}

	storagePath = e.StoragePath(bucketName, key)
	fullPath = filepath.Clean(filepath.Join(e.dataDir, storagePath))

	if !isWithinBase(e.dataDir, fullPath) {
		return "", "", ErrPathTraversal
	}

	return storagePath, fullPath, nil

}

func (e *engine) resolveStoragePath(storagePath string) (string, error) {
	if strings.TrimSpace(storagePath) == "" {
		return "", ErrInvalidKey
	}
	if filepath.IsAbs(storagePath) {
		return "", ErrInvalidKey
	}
	cleaned := filepath.Clean(storagePath)
	fullPath := filepath.Clean(filepath.Join(e.dataDir, cleaned))
	if !isWithinBase(e.dataDir, fullPath) {
		return "", ErrPathTraversal
	}
	return fullPath, nil
}

func isWithinBase(base, target string) bool {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return false
	}
	if rel == ".." {
		return false
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func containsTraversalSegment(key string) bool {
	normalized := strings.ReplaceAll(key, "\\", "/")
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}
