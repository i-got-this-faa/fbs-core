package s3

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

type ObjectHandlers struct {
	Buckets metadata.BucketRepository
	Objects metadata.ObjectRepository
	Storage storage.DiskEngine
	Now     func() time.Time
	NewID   func() string
	Logger  *slog.Logger
}

func (h *ObjectHandlers) PutObject(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if !h.ensureBucket(w, r, bucketName) {
		return
	}
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	pipeline, err := newChecksumPipeline(r.Header)
	if err != nil {
		if errors.Is(err, errInvalidDigest) {
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidDigest, messageInvalidDigest)
			return
		}
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	storagePath, size, err := h.Storage.Write(r.Context(), bucketName, key, pipeline.Reader(r.Body))
	if err != nil {
		h.writeStorageMutationError(w, r, err, bucketName, key, "")
		return
	}

	if err := pipeline.Validate(); err != nil {
		_ = h.Storage.Delete(r.Context(), storagePath)
		WriteS3Error(w, r, http.StatusBadRequest, codeBadDigest, messageBadDigest)
		return
	}

	now := h.now()
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	obj := &metadata.Object{
		ID:          h.newID(),
		BucketName:  bucketName,
		Key:         key,
		Size:        size,
		ETag:        pipeline.ETag(),
		ContentType: contentType,
		StoragePath: storagePath,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := h.Objects.Create(r.Context(), obj); err != nil {
		h.logError("create object metadata", err, bucketName, key, storagePath)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	w.Header().Set("ETag", quoteETag(obj.ETag))
	w.WriteHeader(http.StatusOK)
}

func (h *ObjectHandlers) GetObject(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	obj, ok := h.loadObjectForRead(w, r, bucketName, key)
	if !ok {
		return
	}

	file, err := h.Storage.Open(r.Context(), obj.StoragePath)
	if err != nil {
		mapStorageReadError(w, r, h, err, obj)
		return
	}
	defer file.Close()

	setObjectHeaders(w, obj)
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, file); err != nil {
		h.logError("stream object body", err, obj.BucketName, obj.Key, obj.StoragePath)
	}
}

func (h *ObjectHandlers) HeadObject(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	obj, ok := h.loadObjectForRead(w, r, bucketName, key)
	if !ok {
		return
	}

	file, err := h.Storage.Open(r.Context(), obj.StoragePath)
	if err != nil {
		mapStorageReadError(w, r, h, err, obj)
		return
	}
	defer file.Close()

	setObjectHeaders(w, obj)
	w.WriteHeader(http.StatusOK)
}

func (h *ObjectHandlers) DeleteObject(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if !h.ensureBucket(w, r, bucketName) {
		return
	}
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	obj, err := h.Objects.GetByKey(r.Context(), bucketName, key)
	if errors.Is(err, metadata.ErrObjectNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		h.logError("load object before delete", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	if err := h.Objects.Delete(r.Context(), bucketName, key); err != nil {
		h.logError("delete object metadata", err, bucketName, key, obj.StoragePath)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	if err := h.Storage.Delete(r.Context(), obj.StoragePath); err != nil {
		h.logError("delete object backing file", err, bucketName, key, obj.StoragePath)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ObjectHandlers) ensureBucket(w http.ResponseWriter, r *http.Request, bucketName string) bool {
	if strings.TrimSpace(bucketName) == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return false
	}

	_, err := h.Buckets.GetByName(r.Context(), bucketName)
	if errors.Is(err, metadata.ErrBucketNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchBucket, messageNoSuchBucket)
		return false
	}
	if err != nil {
		h.logError("load bucket", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return false
	}

	return true
}

func (h *ObjectHandlers) writeStorageMutationError(w http.ResponseWriter, r *http.Request, err error, bucketName, key, storagePath string) {
	if errors.Is(err, storage.ErrInvalidKey) || errors.Is(err, storage.ErrPathTraversal) {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	h.logError("mutate object backing file", err, bucketName, key, storagePath)
	WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
}

func (h *ObjectHandlers) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func (h *ObjectHandlers) newID() string {
	if h.NewID != nil {
		return h.NewID()
	}
	return uuid.NewString()
}

func (h *ObjectHandlers) logError(message string, err error, bucketName, key, storagePath string) {
	if h.Logger == nil {
		return
	}
	h.Logger.Error(message, "error", err, "bucket", bucketName, "key", key, "storage_path", storagePath)
}

func objectRouteParams(r *http.Request) (string, string) {
	return chi.URLParam(r, "bucket"), strings.TrimPrefix(chi.URLParam(r, "*"), "/")
}

func quoteETag(etag string) string {
	trimmed := strings.TrimSpace(etag)
	if strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`) {
		return trimmed
	}
	return `"` + trimmed + `"`
}
