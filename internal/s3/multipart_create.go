package s3

import (
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/i-got-this-faa/fbs/internal/iam"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

// CreateMultipartUpload handles POST /{bucket}/{key}?uploads.
func (h *ObjectHandlers) CreateMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if !h.ensureBucketAction(w, r, bucketName, iam.ActionPutObject, key, "") {
		return
	}
	if err := storage.ValidateKey(key); err != nil {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Read the x-amz-checksum-algorithm header if provided.
	checksumAlgo := strings.TrimSpace(r.Header.Get("x-amz-checksum-algorithm"))
	switch checksumAlgo {
	case "SHA256", "SHA1", "CRC32", "CRC32C", "CRC64NVME", "":
		// valid values
	default:
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidArgument, messageInvalidArgument)
		return
	}

	upload := &metadata.MultipartUpload{
		ID:                h.newID(),
		BucketName:        bucketName,
		Key:               key,
		ContentType:       contentType,
		ChecksumAlgorithm: checksumAlgo,
		Status:            metadata.MultipartUploadStatusActive,
		CreatedAt:         h.now(),
		UserMetadata:      parseMetadataHeaders(r),
	}
	if err := h.MultipartUploads.Create(r.Context(), upload); err != nil {
		h.logError("create multipart upload", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	if checksumAlgo != "" {
		w.Header().Set("x-amz-checksum-algorithm", checksumAlgo)
	}
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(InitiateMultipartUploadResult{
		Bucket:            bucketName,
		Key:               key,
		UploadID:          upload.ID,
		ChecksumAlgorithm: checksumAlgo,
		Xmlns:             "http://s3.amazonaws.com/doc/2006-03-01/",
	})
}
