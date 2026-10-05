package s3

import (
	"net/http"

	"github.com/i-got-this-faa/fbs/internal/iam"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

// AbortMultipartUpload handles DELETE /{bucket}/{key}?uploadId={id}.
func (h *ObjectHandlers) AbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucketName, key, uploadID, ok := h.multipartTarget(w, r, iam.ActionAbortMultipartUpload)
	if !ok {
		return
	}
	if _, ok := h.loadUpload(w, r, uploadID, bucketName, key); !ok {
		return
	}
	_, release, ok := h.lockUpload(w, r, uploadID, bucketName, key)
	if !ok {
		return
	}
	defer release()

	claim, ok := h.claimUpload(w, r, uploadID, metadata.MultipartUploadStatusAborted, bucketName, key)
	if !ok {
		return
	}
	defer claim.release()

	if err := h.MultipartUploads.Delete(r.Context(), uploadID); err != nil {
		h.logError("delete multipart upload", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	claim.settle()

	h.deleteUploadParts(uploadID, bucketName, key)
	w.WriteHeader(http.StatusNoContent)
}

// deleteUploadParts removes the part files of a finished upload. Metadata is
// already gone, so a failure only leaves files for startup reconciliation.
func (h *ObjectHandlers) deleteUploadParts(uploadID, bucketName, key string) {
	ctx, cancel := withCleanupTimeout()
	defer cancel()
	if err := h.Storage.DeleteUploadParts(ctx, uploadID); err != nil {
		h.logError("delete upload parts", err, bucketName, key, "")
	}
}
