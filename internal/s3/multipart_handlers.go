package s3

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

const cleanupTimeout = 30 * time.Second

// CreateMultipartUpload handles POST /{bucket}/{key}?uploads.
func (h *ObjectHandlers) CreateMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if !h.ensureBucket(w, r, bucketName) {
		return
	}
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
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

	upload := &metadata.MultipartUpload{
		ID:          h.newID(),
		BucketName:  bucketName,
		Key:         key,
		ContentType: contentType,
		Status:      metadata.MultipartUploadStatusActive,
		CreatedAt:   h.now(),
	}
	if err := h.MultipartUploads.Create(r.Context(), upload); err != nil {
		h.logError("create multipart upload", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(InitiateMultipartUploadResult{
		Bucket:   bucketName,
		Key:      key,
		UploadID: upload.ID,
	})
}

// UploadPart handles PUT /{bucket}/{key}?partNumber={n}&uploadId={id}.
func (h *ObjectHandlers) UploadPart(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if !h.ensureBucket(w, r, bucketName) {
		return
	}
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	q := r.URL.Query()
	uploadID := q.Get("uploadId")
	partNumberStr := q.Get("partNumber")

	if uploadID == "" || partNumberStr == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	partNumber, err := strconv.Atoi(partNumberStr)
	if err != nil || partNumber < 1 || partNumber > 10000 {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	upload, err := h.MultipartUploads.GetByID(r.Context(), uploadID)
	if errors.Is(err, metadata.ErrMultipartUploadNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	} else if err != nil {
		h.logError("get multipart upload", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	if upload.BucketName != bucketName || upload.Key != key {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	}

	release := h.acquireUploadLock(uploadID)
	defer release()

	// Re-read upload after locking to detect races with complete/abort.
	upload, err = h.MultipartUploads.GetByID(r.Context(), uploadID)
	if errors.Is(err, metadata.ErrMultipartUploadNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	} else if err != nil {
		h.logError("re-read multipart upload after lock", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	if upload.BucketName != bucketName || upload.Key != key {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
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

	storagePath, size, err := h.Storage.WritePart(r.Context(), uploadID, partNumber, pipeline.Reader(r.Body))
	if err != nil {
		h.writeStorageMutationError(w, r, err, bucketName, key, "")
		return
	}

	if err := pipeline.Validate(); err != nil {
		ctx, cancel := withCleanupTimeout()
		_ = h.Storage.Delete(ctx, storagePath)
		cancel()
		WriteS3Error(w, r, http.StatusBadRequest, codeBadDigest, messageBadDigest)
		return
	}

	part := &metadata.MultipartPart{
		UploadID:    uploadID,
		PartNumber:  partNumber,
		Size:        size,
		ETag:        pipeline.ETag(),
		StoragePath: storagePath,
		CreatedAt:   h.now(),
	}
	oldStoragePath, err := h.MultipartUploads.AddPart(r.Context(), part)
	if err != nil {
		ctx, cancel := withCleanupTimeout()
		_ = h.Storage.Delete(ctx, storagePath)
		cancel()
		if errors.Is(err, metadata.ErrUploadAlreadyClaimed) || errors.Is(err, metadata.ErrMultipartUploadNotFound) {
			WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
			return
		}
		h.logError("add multipart part metadata", err, bucketName, key, storagePath)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	if oldStoragePath != "" && oldStoragePath != storagePath {
		ctx, cancel := withCleanupTimeout()
		err := h.Storage.Delete(ctx, oldStoragePath)
		cancel()
		if err != nil {
			h.logError("delete old part file after re-upload", err, bucketName, key, oldStoragePath)
		}
	}

	w.Header().Set("ETag", quoteETag(part.ETag))
	w.WriteHeader(http.StatusOK)
}

// CompleteMultipartUpload handles POST /{bucket}/{key}?uploadId={id}.
func (h *ObjectHandlers) CompleteMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if !h.ensureBucket(w, r, bucketName) {
		return
	}
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	uploadID := r.URL.Query().Get("uploadId")
	if uploadID == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	upload, err := h.MultipartUploads.GetByID(r.Context(), uploadID)
	if errors.Is(err, metadata.ErrMultipartUploadNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	} else if err != nil {
		h.logError("get multipart upload", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	if upload.BucketName != bucketName || upload.Key != key {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	}

	release := h.acquireUploadLock(uploadID)
	defer release()

	// Re-read upload after locking to detect races with abort/complete.
	upload, err = h.MultipartUploads.GetByID(r.Context(), uploadID)
	if errors.Is(err, metadata.ErrMultipartUploadNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	} else if err != nil {
		h.logError("re-read multipart upload after lock", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	if upload.BucketName != bucketName || upload.Key != key {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	}

	claimed := false
	completed := false
	defer func() {
		if claimed && !completed {
			ctx, cancel := withCleanupTimeout()
			defer cancel()
			if err := h.MultipartUploads.SetUploadStatus(ctx, uploadID, metadata.MultipartUploadStatusActive); err != nil {
				h.logError("reset upload status after failed completion", err, bucketName, key, "")
			}
		}
	}()

	// Claim the upload so no other process can add parts while we list/assemble.
	if err := h.MultipartUploads.ClaimUpload(r.Context(), uploadID, metadata.MultipartUploadStatusCompleting); err != nil {
		if errors.Is(err, metadata.ErrMultipartUploadNotFound) || errors.Is(err, metadata.ErrUploadAlreadyClaimed) {
			WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
			return
		}
		h.logError("claim multipart upload for completion", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	claimed = true

	var req CompleteMultipartUpload
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteS3Error(w, r, http.StatusBadRequest, codeMalformedXML, messageMalformedXML)
		return
	}

	if len(req.Parts) == 0 {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	// Verify ascending order and no duplicates.
	for i := 1; i < len(req.Parts); i++ {
		if req.Parts[i].PartNumber <= req.Parts[i-1].PartNumber {
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidPartOrder, messageInvalidPartOrder)
			return
		}
	}

	storedParts, err := h.MultipartUploads.ListParts(r.Context(), uploadID)
	if err != nil {
		h.logError("list multipart parts", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	partMap := make(map[int]metadata.MultipartPart, len(storedParts))
	for _, p := range storedParts {
		partMap[p.PartNumber] = p
	}

	minPartSize := h.MinPartSize
	if minPartSize == 0 {
		minPartSize = 5 * 1024 * 1024 // 5 MiB
	}

	partPaths := make([]string, 0, len(req.Parts))
	partETags := make([]string, 0, len(req.Parts))
	for i, cp := range req.Parts {
		sp, ok := partMap[cp.PartNumber]
		if !ok {
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidPart, messageInvalidPart)
			return
		}
		if !etagMatches(cp.ETag, sp.ETag) {
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidPart, messageInvalidPart)
			return
		}
		if i < len(req.Parts)-1 && sp.Size < minPartSize {
			WriteS3Error(w, r, http.StatusBadRequest, codeEntityTooSmall, messageEntityTooSmall)
			return
		}
		partPaths = append(partPaths, sp.StoragePath)
		partETags = append(partETags, sp.ETag)
	}

	storagePath, size, err := h.Storage.AssembleParts(r.Context(), bucketName, key, partPaths)
	if err != nil {
		h.writeStorageMutationError(w, r, err, bucketName, key, "")
		return
	}

	etag, err := MultipartETag(partETags)
	if err != nil {
		h.logError("compute multipart etag", err, bucketName, key, storagePath)
		ctx, cancel := withCleanupTimeout()
		_ = h.Storage.Delete(ctx, storagePath)
		cancel()
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	now := h.now()
	contentType := upload.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	obj := &metadata.Object{
		ID:          h.newID(),
		BucketName:  bucketName,
		Key:         key,
		Size:        size,
		ETag:        etag,
		ContentType: contentType,
		StoragePath: storagePath,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// CompleteUpload atomically verifies the upload still exists, creates the
	// object, deletes the upload, and returns the previous object storage path.
	oldStoragePath, err := h.MultipartUploads.CompleteUpload(r.Context(), obj, uploadID)
	if err != nil {
		if errors.Is(err, metadata.ErrMultipartUploadNotFound) {
			claimed = false
			ctx, cancel := withCleanupTimeout()
			_ = h.Storage.Delete(ctx, storagePath)
			cancel()
			WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
			return
		}
		if errors.Is(err, metadata.ErrUploadAlreadyClaimed) {
			// Another process completed or aborted the upload while we were
			// assembling. Let the deferred reset run to recover our claim.
			ctx, cancel := withCleanupTimeout()
			_ = h.Storage.Delete(ctx, storagePath)
			cancel()
			WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
			return
		}
		h.logError("complete multipart upload", err, bucketName, key, storagePath)
		ctx, cancel := withCleanupTimeout()
		_ = h.Storage.Delete(ctx, storagePath)
		cancel()
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	claimed = false
	completed = true

	if err := h.recordActivity(r.Context(), bucketName, key); err != nil {
		h.logError("record object activity after multipart complete", err, bucketName, key, "")
	}

	if oldStoragePath != "" && oldStoragePath != storagePath {
		ctx, cancel := withCleanupTimeout()
		err := h.Storage.Delete(ctx, oldStoragePath)
		cancel()
		if err != nil {
			h.logError("delete old object backing file after multipart complete", err, bucketName, key, oldStoragePath)
		}
	}

	cleanupCtx, cancel := withCleanupTimeout()
	if err := h.Storage.DeleteUploadParts(cleanupCtx, uploadID); err != nil {
		h.logError("delete upload parts after complete", err, bucketName, key, "")
	}
	cancel()

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(CompleteMultipartUploadResult{
		Location: fmt.Sprintf("/%s/%s", bucketName, key),
		Bucket:   bucketName,
		Key:      key,
		ETag:     quoteETag(etag),
	})
}

// AbortMultipartUpload handles DELETE /{bucket}/{key}?uploadId={id}.
func (h *ObjectHandlers) AbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if !h.ensureBucket(w, r, bucketName) {
		return
	}
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	uploadID := r.URL.Query().Get("uploadId")
	if uploadID == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	upload, err := h.MultipartUploads.GetByID(r.Context(), uploadID)
	if errors.Is(err, metadata.ErrMultipartUploadNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	} else if err != nil {
		h.logError("get multipart upload for abort", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	if upload.BucketName != bucketName || upload.Key != key {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	}

	release := h.acquireUploadLock(uploadID)
	defer release()

	// Re-read upload after locking to detect races with complete/abort.
	upload, err = h.MultipartUploads.GetByID(r.Context(), uploadID)
	if errors.Is(err, metadata.ErrMultipartUploadNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	} else if err != nil {
		h.logError("re-read multipart upload after lock", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	if upload.BucketName != bucketName || upload.Key != key {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	}

	claimed := false
	defer func() {
		if claimed {
			ctx, cancel := withCleanupTimeout()
			defer cancel()
			if err := h.MultipartUploads.SetUploadStatus(ctx, uploadID, metadata.MultipartUploadStatusActive); err != nil {
				h.logError("reset upload status after failed abort", err, bucketName, key, "")
			}
		}
	}()

	// Claim the upload so no other process can add parts while we abort.
	if err := h.MultipartUploads.ClaimUpload(r.Context(), uploadID, metadata.MultipartUploadStatusAborted); err != nil {
		if errors.Is(err, metadata.ErrMultipartUploadNotFound) || errors.Is(err, metadata.ErrUploadAlreadyClaimed) {
			// Return NoSuchUpload when the upload is missing or already claimed.
			// S3 clients treat this as "the upload is gone", which is correct
			// regardless of whether it was completed or aborted by another process.
			WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
			return
		}
		h.logError("claim multipart upload for abort", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	claimed = true

	if err := h.MultipartUploads.Delete(r.Context(), uploadID); err != nil {
		h.logError("delete multipart upload", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	claimed = false

	cleanupCtx, cancel := withCleanupTimeout()
	if err := h.Storage.DeleteUploadParts(cleanupCtx, uploadID); err != nil {
		h.logError("delete upload parts", err, bucketName, key, "")
	}
	cancel()

	w.WriteHeader(http.StatusNoContent)
}

// DispatchPut routes PUT requests to either PutObject or UploadPart.
func (h *ObjectHandlers) DispatchPut(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hasUploadID := q.Has("uploadId")
	hasPartNumber := q.Has("partNumber")
	if hasUploadID && hasPartNumber && q.Get("uploadId") != "" && q.Get("partNumber") != "" {
		h.UploadPart(w, r)
		return
	}
	if hasUploadID || hasPartNumber {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	h.PutObject(w, r)
}

// DispatchPost routes POST requests to either CreateMultipartUpload or CompleteMultipartUpload.
func (h *ObjectHandlers) DispatchPost(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Has("uploads") {
		h.CreateMultipartUpload(w, r)
		return
	}
	if q.Get("uploadId") != "" {
		h.CompleteMultipartUpload(w, r)
		return
	}
	WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
}

// DispatchDelete routes DELETE requests to either DeleteObject or AbortMultipartUpload.
func (h *ObjectHandlers) DispatchDelete(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Has("uploadId") {
		if q.Get("uploadId") == "" {
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
			return
		}
		h.AbortMultipartUpload(w, r)
		return
	}
	h.DeleteObject(w, r)
}

func etagMatches(requestETag, storedETag string) bool {
	return strings.EqualFold(unquoteETag(requestETag), unquoteETag(storedETag))
}

// StaleMultipartCleanup periodically removes multipart uploads older than ttl.
func StaleMultipartCleanup(ctx context.Context, uploads metadata.MultipartUploadRepository, store storage.DiskEngine, ttl time.Duration, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().UTC().Add(-ttl)
			stale, err := uploads.ListStale(ctx, cutoff)
			if err != nil {
				if logger != nil {
					logger.Error("list stale multipart uploads", "error", err)
				}
				continue
			}
			for _, u := range stale {
				// Non-active uploads get a longer grace period before forced
				// deletion, in case a long-running completion is still assembling.
				if u.Status != metadata.MultipartUploadStatusActive {
					if time.Since(u.StatusUpdatedAt) < ttl*2 {
						continue
					}
				}

				claimed := false
				if u.Status == metadata.MultipartUploadStatusActive {
					// Claim active uploads first so we do not race with active operations.
					if err := uploads.ClaimUpload(ctx, u.ID, metadata.MultipartUploadStatusAborted); err != nil {
						if logger != nil {
							logger.Error("claim stale multipart upload", "upload_id", u.ID, "error", err)
						}
						continue
					}
					claimed = true
				}
				// Delete metadata first so rows never point to missing files.
				if err := uploads.Delete(ctx, u.ID); err != nil {
					if logger != nil {
						logger.Error("delete stale multipart upload", "upload_id", u.ID, "error", err)
					}
					if claimed {
						resetCtx, cancel := withCleanupTimeout()
						resetErr := uploads.SetUploadStatus(resetCtx, u.ID, metadata.MultipartUploadStatusActive)
						cancel()
						if resetErr != nil {
							if logger != nil {
								logger.Error("reset upload status after failed stale delete", "upload_id", u.ID, "error", resetErr)
							}
						}
					}
					continue
				}
				cleanupCtx, cancel := withCleanupTimeout()
				if err := store.DeleteUploadParts(cleanupCtx, u.ID); err != nil {
					if logger != nil {
						logger.Error("delete stale upload parts", "upload_id", u.ID, "error", err)
					}
				}
				cancel()
			}
		}
	}
}

func withCleanupTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), cleanupTimeout)
}
