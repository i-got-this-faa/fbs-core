package s3

import (
	"errors"
	"net/http"
	"strings"

	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func (h *ObjectHandlers) DeleteBucket(w http.ResponseWriter, r *http.Request) {
	bucketName := chiBucketParam(r)
	if !h.ensureBucket(w, r, bucketName) {
		return
	}

	objects, isTruncated, err := h.Objects.List(r.Context(), bucketName, "", "", 1)
	if err != nil {
		h.logError("check bucket emptiness", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	if len(objects) > 0 || isTruncated {
		WriteS3Error(w, r, http.StatusConflict, codeBucketNotEmpty, messageBucketNotEmpty)
		return
	}

	err = h.Buckets.Delete(r.Context(), bucketName)
	if errors.Is(err, metadata.ErrBucketNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchBucket, messageNoSuchBucket)
		return
	}
	if err != nil {
		// Multipart upload rows (and similar dependents) can still reference the
		// bucket after objects are gone. Map FK failures to BucketNotEmpty
		// instead of leaking a 500.
		if isSQLiteForeignKeyError(err) {
			WriteS3Error(w, r, http.StatusConflict, codeBucketNotEmpty, messageBucketNotEmpty)
			return
		}
		h.logError("delete bucket", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	h.recordActivity(r, "delete_bucket", bucketName, "", 0, "")
	w.WriteHeader(http.StatusNoContent)
}

func isSQLiteForeignKeyError(err error) bool {
	if err == nil {
		return false
	}
	// SQLite: "FOREIGN KEY constraint failed" (and variants). Do not match
	// other constraint types (e.g. UNIQUE) as BucketNotEmpty.
	return strings.Contains(strings.ToLower(err.Error()), "foreign key")
}
