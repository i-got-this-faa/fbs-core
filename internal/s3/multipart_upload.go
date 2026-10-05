package s3

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/i-got-this-faa/fbs/internal/iam"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

// cleanupTimeout bounds best-effort cleanup that must outlive the request context.
const cleanupTimeout = 30 * time.Second

const (
	defaultMinPartSize = 5 * 1024 * 1024
	maxPartNumber      = 10000
	maxListResults     = 1000
)

func withCleanupTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), cleanupTimeout)
}

// multipartTarget authorizes action on the request's object key and returns
// the uploadId query value. It writes the error response when ok is false.
func (h *ObjectHandlers) multipartTarget(w http.ResponseWriter, r *http.Request, action iam.Action) (bucketName, key, uploadID string, ok bool) {
	bucketName, key = objectRouteParams(r)
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return "", "", "", false
	}
	if !h.ensureBucketAction(w, r, bucketName, action, key, "") {
		return "", "", "", false
	}
	uploadID = r.URL.Query().Get("uploadId")
	if uploadID == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return "", "", "", false
	}
	return bucketName, key, uploadID, true
}

// parsePartNumber parses the partNumber query value. S3 allows parts 1-10000.
func parsePartNumber(w http.ResponseWriter, r *http.Request) (int, bool) {
	partNumber, err := strconv.Atoi(r.URL.Query().Get("partNumber"))
	if err != nil || partNumber < 1 || partNumber > maxPartNumber {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return 0, false
	}
	return partNumber, true
}

// parseListLimit parses a max-uploads or max-parts value. A missing or zero
// value means the default, and every value is capped at 1000.
func parseListLimit(w http.ResponseWriter, r *http.Request, raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return maxListResults, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidArgument, messageInvalidArgument)
		return 0, false
	}
	if limit == 0 || limit > maxListResults {
		return maxListResults, true
	}
	return limit, true
}

// loadUpload returns the upload when it exists and belongs to bucketName/key.
// Any other upload is reported as NoSuchUpload.
func (h *ObjectHandlers) loadUpload(w http.ResponseWriter, r *http.Request, uploadID, bucketName, key string) (*metadata.MultipartUpload, bool) {
	upload, err := h.MultipartUploads.GetByID(r.Context(), uploadID)
	if err != nil && !errors.Is(err, metadata.ErrMultipartUploadNotFound) {
		h.logError("load multipart upload", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return nil, false
	}
	if err != nil || upload.BucketName != bucketName || upload.Key != key {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return nil, false
	}
	return upload, true
}

// lockUpload takes the in-process lock for uploadID and re-reads the upload,
// because a concurrent complete or abort may have removed it while the caller
// waited. When ok is true the caller must call release.
func (h *ObjectHandlers) lockUpload(w http.ResponseWriter, r *http.Request, uploadID, bucketName, key string) (upload *metadata.MultipartUpload, release func(), ok bool) {
	release = h.acquireUploadLock(uploadID)
	upload, ok = h.loadUpload(w, r, uploadID, bucketName, key)
	if !ok {
		release()
		return nil, nil, false
	}
	return upload, release, true
}

// deleteStoredFile removes a backing file outside the request context and
// logs a failure. reason names the cleanup in the log.
func (h *ObjectHandlers) deleteStoredFile(storagePath, reason, bucketName, key string) {
	ctx, cancel := withCleanupTimeout()
	defer cancel()
	if err := h.Storage.Delete(ctx, storagePath); err != nil {
		h.logError(reason, err, bucketName, key, storagePath)
	}
}

// storePart records an uploaded part file. On failure it removes the new file;
// on success it removes the file of the part it replaced.
func (h *ObjectHandlers) storePart(w http.ResponseWriter, r *http.Request, part *metadata.MultipartPart, bucketName, key string) bool {
	replacedPath, err := h.MultipartUploads.AddPart(r.Context(), part)
	if err != nil {
		h.deleteStoredFile(part.StoragePath, "discard part file after failed metadata write", bucketName, key)
		if errors.Is(err, metadata.ErrUploadAlreadyClaimed) || errors.Is(err, metadata.ErrMultipartUploadNotFound) {
			WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
			return false
		}
		h.logError("add multipart part metadata", err, bucketName, key, part.StoragePath)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return false
	}
	if replacedPath != "" && replacedPath != part.StoragePath {
		h.deleteStoredFile(replacedPath, "delete replaced part file", bucketName, key)
	}
	return true
}

// uploadClaim moves an upload out of the active state so no part can be added
// while the caller completes or aborts it. Unless the claim is settled, release
// returns the upload to active so the client can retry.
type uploadClaim struct {
	h          *ObjectHandlers
	uploadID   string
	bucketName string
	key        string
	settled    bool
}

// claimUpload claims the upload for status. A missing or already-claimed
// upload is reported as NoSuchUpload: for the client the upload is gone,
// whether another request completed or aborted it.
func (h *ObjectHandlers) claimUpload(w http.ResponseWriter, r *http.Request, uploadID string, status metadata.MultipartUploadStatus, bucketName, key string) (*uploadClaim, bool) {
	err := h.MultipartUploads.ClaimUpload(r.Context(), uploadID, status)
	if errors.Is(err, metadata.ErrMultipartUploadNotFound) || errors.Is(err, metadata.ErrUploadAlreadyClaimed) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return nil, false
	}
	if err != nil {
		h.logError("claim multipart upload", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return nil, false
	}
	return &uploadClaim{h: h, uploadID: uploadID, bucketName: bucketName, key: key}, true
}

// settle marks the claim as final, so release leaves the upload as it is.
func (c *uploadClaim) settle() {
	c.settled = true
}

func (c *uploadClaim) release() {
	if c.settled {
		return
	}
	ctx, cancel := withCleanupTimeout()
	defer cancel()
	if err := c.h.MultipartUploads.SetUploadStatus(ctx, c.uploadID, metadata.MultipartUploadStatusActive); err != nil {
		c.h.logError("reset upload status after failed claim", err, c.bucketName, c.key, "")
	}
}

func etagMatches(requestETag, storedETag string) bool {
	return strings.EqualFold(unquoteETag(requestETag), unquoteETag(storedETag))
}
