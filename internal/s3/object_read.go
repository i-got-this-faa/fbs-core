package s3

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

func (h *ObjectHandlers) loadObjectForRead(w http.ResponseWriter, r *http.Request, bucketName, key string) (*metadata.Object, bool) {
	if !h.ensureBucket(w, r, bucketName) {
		return nil, false
	}
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return nil, false
	}

	obj, err := h.Objects.GetByKey(r.Context(), bucketName, key)
	if errors.Is(err, metadata.ErrObjectNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchKey, messageNoSuchKey)
		return nil, false
	}
	if err != nil {
		h.logError("load object metadata", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return nil, false
	}

	return obj, true
}

type storageReadErrorMapper func(http.ResponseWriter, *http.Request, *ObjectHandlers, error, *metadata.Object)

func (h *ObjectHandlers) serveObject(w http.ResponseWriter, r *http.Request, obj *metadata.Object, cacheControl string, mapReadError storageReadErrorMapper) {
	file, err := h.Storage.Open(r.Context(), obj.StoragePath)
	if err != nil {
		mapReadError(w, r, h, err, obj)
		return
	}
	defer file.Close()

	setObjectHeaders(w, obj, cacheControl)
	http.ServeContent(w, r, obj.Key, obj.UpdatedAt.UTC(), file)
}

func setObjectHeaders(w http.ResponseWriter, obj *metadata.Object, cacheControl string) {
	w.Header().Set("ETag", quoteETag(obj.ETag))
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("Last-Modified", obj.UpdatedAt.UTC().Format(http.TimeFormat))
	if obj.ContentType != "" {
		w.Header().Set("Content-Type", obj.ContentType)
	}
	if cacheControl != "" {
		w.Header().Set("Cache-Control", cacheControl)
	}
}

func mapAuthenticatedStorageReadError(w http.ResponseWriter, r *http.Request, h *ObjectHandlers, err error, obj *metadata.Object) {
	if errors.Is(err, storage.ErrNotFound) {
		h.logError("object metadata exists but backing file is missing", err, obj.BucketName, obj.Key, obj.StoragePath)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	h.logError("open object backing file", err, obj.BucketName, obj.Key, obj.StoragePath)
	WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
}

func (h *ObjectHandlers) PublicReadObject(w http.ResponseWriter, r *http.Request) {
	if !h.validatePublicReadSignature(w, r) {
		return
	}

	bucketName, key := objectRouteParams(r)
	if bucketName == "" || key == "" {
		writePublicReadError(w, http.StatusNotFound)
		return
	}

	obj, err := h.Objects.GetByKey(r.Context(), bucketName, key)
	if errors.Is(err, metadata.ErrObjectNotFound) {
		writePublicReadError(w, http.StatusNotFound)
		return
	}
	if err != nil {
		h.logError("load public object metadata", err, bucketName, key, "")
		writePublicReadError(w, http.StatusInternalServerError)
		return
	}

	h.serveObject(w, r, obj, h.publicCacheControl(r), mapPublicStorageReadError)
}

func (h *ObjectHandlers) validatePublicReadSignature(w http.ResponseWriter, r *http.Request) bool {
	query := r.URL.Query()
	if h.PublicReadSigner == nil || len(query) != 2 || len(query["expires"]) != 1 || len(query["signature"]) != 1 {
		writePublicReadError(w, http.StatusForbidden)
		return false
	}

	expires := query.Get("expires")
	signature := query.Get("signature")
	if err := h.PublicReadSigner.Verify(r.URL.EscapedPath(), expires, signature); err != nil {
		writePublicReadError(w, http.StatusForbidden)
		return false
	}

	return true
}

func (h *ObjectHandlers) publicCacheControl(r *http.Request) string {
	expiresUnix, err := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	if err != nil {
		return "public, max-age=0, must-revalidate"
	}

	remaining := time.Unix(expiresUnix, 0).Sub(h.now())
	if remaining <= 0 {
		return "public, max-age=0, must-revalidate"
	}

	maxAge := int64(remaining.Truncate(time.Second).Seconds())
	return fmt.Sprintf("public, max-age=%d, must-revalidate", maxAge)
}

func mapPublicStorageReadError(w http.ResponseWriter, r *http.Request, h *ObjectHandlers, err error, obj *metadata.Object) {
	if errors.Is(err, storage.ErrNotFound) {
		h.logError("public object metadata exists but backing file is missing", err, obj.BucketName, obj.Key, obj.StoragePath)
		writePublicReadError(w, http.StatusInternalServerError)
		return
	}

	h.logError("open public object backing file", err, obj.BucketName, obj.Key, obj.StoragePath)
	writePublicReadError(w, http.StatusInternalServerError)
}

func writePublicReadError(w http.ResponseWriter, statusCode int) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, http.StatusText(statusCode), statusCode)
}
