package storage 

import "errors"

var (
	ErrNotFound      = errors.New("storage: file not found")
	ErrInvalidKey    = errors.New("storage: invalid object key")
	ErrPathTraversal = errors.New("storage: path traversal detected")
)