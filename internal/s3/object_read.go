package s3

import (
	"errors"
	"net/http"
	"strconv"

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

func setObjectHeaders(w http.ResponseWriter, obj *metadata.Object) {
	w.Header().Set("ETag", quoteETag(obj.ETag))
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("Last-Modified", obj.UpdatedAt.UTC().Format(http.TimeFormat))
	w.Header().Set("Content-Type", obj.ContentType)
}

func mapStorageReadError(w http.ResponseWriter, r *http.Request, h *ObjectHandlers, err error, obj *metadata.Object) {
	if errors.Is(err, storage.ErrNotFound) {
		h.logError("object metadata exists but backing file is missing", err, obj.BucketName, obj.Key, obj.StoragePath)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	h.logError("open object backing file", err, obj.BucketName, obj.Key, obj.StoragePath)
	WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
}
