package s3

import (
	"errors"
	"net/http"

	sqlite "modernc.org/sqlite"
	lib "modernc.org/sqlite/lib"

	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func (h *ObjectHandlers) DeleteBucket(w http.ResponseWriter, r *http.Request) {
	bucketName := chiBucketParam(r)
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionDeleteBucket, "", "") {
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

	// Abort any pending multipart uploads before deleting the bucket.
	// S3 allows deleting buckets with pending multipart uploads, but our FK
	// constraint requires them to be removed first. Paginate through all
	// uploads since there may be more than 1000.
	const maxAbortIterations = 10 // safety ceiling (10K uploads at 1K/batch)
	for range maxAbortIterations {
		uploads, _, _, _, listErr := h.MultipartUploads.ListByBucket(r.Context(), bucketName, "", "", "", 1000)
		if listErr != nil {
			h.logError("list multipart uploads for bucket deletion", listErr, bucketName, "", "")
			WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
			return
		}
		if len(uploads) == 0 {
			break
		}
		for _, upload := range uploads {
			if delErr := h.MultipartUploads.Delete(r.Context(), upload.ID); delErr != nil {
				h.logError("delete multipart upload during bucket deletion", delErr, bucketName, upload.Key, upload.ID)
				WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
				return
			}
			ctx, cancel := withCleanupTimeout()
			if storErr := h.Storage.DeleteUploadParts(ctx, upload.ID); storErr != nil {
				h.logError("delete upload parts during bucket deletion", storErr, bucketName, upload.Key, upload.ID)
			}
			cancel()
		}
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
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code() == lib.SQLITE_CONSTRAINT_FOREIGNKEY
	}
	return false
}
