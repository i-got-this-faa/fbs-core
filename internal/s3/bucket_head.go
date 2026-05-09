package s3

import (
	"errors"
	"net/http"

	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/s3compat"
)

func (h *ObjectHandlers) HeadBucket(w http.ResponseWriter, r *http.Request) {
	bucketName := chiBucketParam(r)
	if bucketName == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	_, err := h.Buckets.GetByName(r.Context(), bucketName)
	if errors.Is(err, metadata.ErrBucketNotFound) {
		w.Header().Set("x-amz-bucket-region", s3compat.Region)
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchBucket, messageNoSuchBucket)
		return
	}
	if err != nil {
		h.logError("head bucket", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	w.Header().Set("x-amz-bucket-region", s3compat.Region)
	w.WriteHeader(http.StatusOK)
}
