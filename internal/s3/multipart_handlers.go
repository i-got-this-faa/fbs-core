package s3

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

const cleanupTimeout = 30 * time.Second

// CreateMultipartUpload handles POST /{bucket}/{key}?uploads.
func (h *ObjectHandlers) CreateMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionPutObject, key, "") {
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

// ListMultipartUploads handles GET /{bucket}?uploads.
func (h *ObjectHandlers) ListMultipartUploads(w http.ResponseWriter, r *http.Request) {
	bucketName := chiBucketParam(r)
	prefix := r.URL.Query().Get("prefix")
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionListBucket, "", prefix) {
		return
	}

	q := r.URL.Query()
	maxUploads := 1000
	if raw := strings.TrimSpace(q.Get("max-uploads")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidArgument, messageInvalidArgument)
			return
		}
		if parsed > 0 {
			maxUploads = parsed
		}
	}
	if maxUploads > 1000 {
		maxUploads = 1000
	}

	prefix = q.Get("prefix")
	keyMarker := q.Get("key-marker")
	uploadIDMarker := q.Get("upload-id-marker")
	delimiter := q.Get("delimiter")

	uploads, isTruncated, nextKeyMarker, nextUploadIDMarker, err := h.MultipartUploads.ListByBucket(
		r.Context(), bucketName, prefix, keyMarker, uploadIDMarker, maxUploads,
	)
	if err != nil {
		h.logError("list multipart uploads", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	entries := make([]MultipartUploadEntry, 0, len(uploads))
	for _, u := range uploads {
		entries = append(entries, MultipartUploadEntry{
			Key:          u.Key,
			UploadID:     u.ID,
			Initiator:    Owner{ID: "anonymous", DisplayName: "anonymous"},
			Owner:        Owner{ID: "anonymous", DisplayName: "anonymous"},
			StorageClass: "STANDARD",
			Initiated:    u.CreatedAt.Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(ListMultipartUploadsResult{
		Xmlns:              "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:             bucketName,
		KeyMarker:          keyMarker,
		UploadIDMarker:     uploadIDMarker,
		NextKeyMarker:      nextKeyMarker,
		NextUploadIDMarker: nextUploadIDMarker,
		MaxUploads:         maxUploads,
		IsTruncated:        isTruncated,
		Upload:             entries,
		Prefix:             prefix,
		Delimiter:          delimiter,
	})
}

// UploadPart handles PUT /{bucket}/{key}?partNumber={n}&uploadId={id}.
func (h *ObjectHandlers) UploadPart(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionPutObject, key, "") {
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

	// Check conditional headers (If-Match / If-None-Match) against the existing object.
	existingObj, _ := h.Objects.GetByKey(r.Context(), bucketName, key)
	if h.checkPreconditionFailed(w, r, existingObj) {
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
	if fmt.Sprint(upload.Status) != "active" {
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

	cksums := pipeline.Checksums()
	part := &metadata.MultipartPart{
		UploadID:         uploadID,
		PartNumber:       partNumber,
		Size:             size,
		ETag:             pipeline.ETag(),
		StoragePath:      storagePath,
		CreatedAt:        h.now(),
		ChecksumCRC32:    cksums["x-amz-checksum-crc32"],
		ChecksumCRC32C:   cksums["x-amz-checksum-crc32c"],
		ChecksumCRC64NVME: cksums["x-amz-checksum-crc64nvme"],
		ChecksumSHA1:     cksums["x-amz-checksum-sha1"],
		ChecksumSHA256:   cksums["x-amz-checksum-sha256"],
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
	if cksums["x-amz-checksum-crc32"] != "" {
		w.Header().Set("x-amz-checksum-crc32", cksums["x-amz-checksum-crc32"])
	}
	if cksums["x-amz-checksum-crc32c"] != "" {
		w.Header().Set("x-amz-checksum-crc32c", cksums["x-amz-checksum-crc32c"])
	}
	if cksums["x-amz-checksum-crc64nvme"] != "" {
		w.Header().Set("x-amz-checksum-crc64nvme", cksums["x-amz-checksum-crc64nvme"])
	}
	if cksums["x-amz-checksum-sha1"] != "" {
		w.Header().Set("x-amz-checksum-sha1", cksums["x-amz-checksum-sha1"])
	}
	if cksums["x-amz-checksum-sha256"] != "" {
		w.Header().Set("x-amz-checksum-sha256", cksums["x-amz-checksum-sha256"])
	}
	w.WriteHeader(http.StatusOK)
}

// ListParts handles GET /{bucket}/{key}?uploadId={id}.
func (h *ObjectHandlers) ListParts(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionPutObject, key, "") {
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
		h.logError("get multipart upload for list parts", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	if upload.BucketName != bucketName || upload.Key != key {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	}

	parts, err := h.MultipartUploads.ListParts(r.Context(), uploadID)
	if err != nil {
		h.logError("list parts", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	q := r.URL.Query()
	maxParts := 1000
	if raw := strings.TrimSpace(q.Get("max-parts")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidArgument, messageInvalidArgument)
			return
		}
		if parsed > 0 {
			maxParts = parsed
		}
	}
	if maxParts > 1000 {
		maxParts = 1000
	}

	partNumberMarker := 0
	if raw := strings.TrimSpace(q.Get("part-number-marker")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidArgument, messageInvalidArgument)
			return
		}
		partNumberMarker = parsed
	}

	// Filter parts by marker.
	startIdx := 0
	for i, p := range parts {
		if p.PartNumber > partNumberMarker {
			startIdx = i
			break
		}
	}
	if startIdx > 0 {
		parts = parts[startIdx:]
	}

	isTruncated := len(parts) > maxParts
	if isTruncated {
		parts = parts[:maxParts]
	}

	partEntries := make([]ListPartsPart, 0, len(parts))
	for _, p := range parts {
		partEntries = append(partEntries, ListPartsPart{
			PartNumber:   p.PartNumber,
			LastModified: p.CreatedAt.Format(time.RFC3339),
			ETag:         quoteETag(p.ETag),
			Size:         p.Size,
		})
	}

	nextMarker := 0
	if isTruncated && len(parts) > 0 {
		nextMarker = parts[len(parts)-1].PartNumber
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(ListPartsResult{
		Xmlns:                "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:               bucketName,
		Key:                  key,
		UploadID:             uploadID,
		Initiator:            Owner{ID: "anonymous", DisplayName: "anonymous"},
		Owner:                Owner{ID: "anonymous", DisplayName: "anonymous"},
		StorageClass:         "STANDARD",
		PartNumberMarker:     partNumberMarker,
		NextPartNumberMarker: nextMarker,
		MaxParts:             maxParts,
		IsTruncated:          isTruncated,
		Part:                 partEntries,
	})
}

// CompleteMultipartUpload handles POST /{bucket}/{key}?uploadId={id}.
func (h *ObjectHandlers) CompleteMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionPutObject, key, "") {
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
		// S3 idempotency: if the upload is gone but the object exists,
		// the upload was already completed — return success as S3 does.
		if existing, getErr := h.Objects.GetByKey(r.Context(), bucketName, key); getErr == nil && existing != nil {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_ = xml.NewEncoder(w).Encode(CompleteMultipartUploadResult{
				Location: fmt.Sprintf("/%s/%s", bucketName, key),
				Bucket:   bucketName,
				Key:      key,
				ETag:     quoteETag(existing.ETag),
				Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
			})
			return
		}
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

	// Check conditional headers (If-Match / If-None-Match) against the existing object.
	existingObj, _ := h.Objects.GetByKey(r.Context(), bucketName, key)
	if h.checkPreconditionFailed(w, r, existingObj) {
		return
	}
	// S3 returns NoSuchKey when If-Match is set and the object does not exist.
	if existingObj == nil && r.Header.Get("If-Match") != "" {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchKey, messageNoSuchKey)
		return
	}

	release := h.acquireUploadLock(uploadID)
	defer release()

	// Re-read upload after locking to detect races with abort/complete.
	upload, err = h.MultipartUploads.GetByID(r.Context(), uploadID)
	if errors.Is(err, metadata.ErrMultipartUploadNotFound) {
		// Same idempotency check after lock acquisition — the upload may have
		// been completed by another process while we were waiting for the lock.
		if existing, getErr := h.Objects.GetByKey(r.Context(), bucketName, key); getErr == nil && existing != nil {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_ = xml.NewEncoder(w).Encode(CompleteMultipartUploadResult{
				Location: fmt.Sprintf("/%s/%s", bucketName, key),
				Bucket:   bucketName,
				Key:      key,
				ETag:     quoteETag(existing.ETag),
				Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
			})
			return
		}
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

	// Deduplicate part numbers: S3 uses the last occurrence for each part number.
	seen := make(map[int]int, len(req.Parts))
	uniqueParts := make([]CompletePart, 0, len(req.Parts))
	for _, p := range req.Parts {
		if idx, ok := seen[p.PartNumber]; ok {
			uniqueParts[idx] = p
		} else {
			seen[p.PartNumber] = len(uniqueParts)
			uniqueParts = append(uniqueParts, p)
		}
	}
	req.Parts = uniqueParts

	// Verify non-decreasing order (duplicates already deduplicated, so this
	// effectively checks strictly ascending with tolerance for dedup).
	for i := 1; i < len(req.Parts); i++ {
		if req.Parts[i].PartNumber < req.Parts[i-1].PartNumber {
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
		ID:           h.newID(),
		BucketName:   bucketName,
		Key:          key,
		Size:         size,
		ETag:         etag,
		ContentType:  contentType,
		StoragePath:  storagePath,
		CreatedAt:    now,
		UpdatedAt:    now,
		IsMultipart:  true,
		PartsCount:   len(req.Parts),
		UserMetadata: upload.UserMetadata,
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

	metadata.PutObjectInCache(h.Objects, obj)
	h.recordActivity(r, "complete_multipart_upload", bucketName, key, size, etag)

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
		Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
	})
}

// AbortMultipartUpload handles DELETE /{bucket}/{key}?uploadId={id}.
func (h *ObjectHandlers) AbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionAbortMultipartUpload, key, "") {
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


// UploadPartCopy handles PUT /{bucket}/{key}?partNumber={n}&uploadId={id} with x-amz-copy-source.
func (h *ObjectHandlers) UploadPartCopy(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionPutObject, key, "") {
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

	copySource := r.Header.Get("x-amz-copy-source")
	if copySource == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	source, err := parseCopySource(copySource)
	if err != nil {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if source.versionID != "" {
		WriteS3Error(w, r, http.StatusNotImplemented, codeNotImplemented, messageNotImplemented)
		return
	}

	// Validate the multipart upload exists and matches the target key.
	upload, err := h.MultipartUploads.GetByID(r.Context(), uploadID)
	if errors.Is(err, metadata.ErrMultipartUploadNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	} else if err != nil {
		h.logError("get multipart upload for copy part", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	if upload.BucketName != bucketName || upload.Key != key {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
		return
	}

	// Ensure the source bucket exists.
	if !h.ensureBucketAction(w, r, source.bucketName, authz.ActionGetObject, source.key, "") {
		return
	}

	// Load source object.
	sourceObject, err := h.Objects.GetByKey(r.Context(), source.bucketName, source.key)
	if errors.Is(err, metadata.ErrObjectNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchKey, messageNoSuchKey)
		return
	} else if err != nil {
		h.logError("get source object for copy part", err, source.bucketName, source.key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	// Open source file.
	sourceFile, err := h.Storage.Open(r.Context(), sourceObject.StoragePath)
	if err != nil {
		h.logError("open source object for copy part", err, source.bucketName, source.key, sourceObject.StoragePath)
		if errors.Is(err, storage.ErrNotFound) {
			WriteS3Error(w, r, http.StatusNotFound, codeNoSuchKey, messageNoSuchKey)
			return
		}
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	defer sourceFile.Close()

	var srcReader io.Reader = sourceFile
	srcSize := sourceObject.Size

	// Handle optional x-amz-copy-source-range.
	if rangeHeader := r.Header.Get("x-amz-copy-source-range"); rangeHeader != "" {
		start, end, err := parseByteRange(rangeHeader, srcSize)
		if err != nil {
			if errors.Is(err, errRangeExceedsSize) {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", srcSize))
				WriteS3Error(w, r, http.StatusRequestedRangeNotSatisfiable, codeInvalidRange, "The requested range is not valid.")
				return
			}
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidArgument, messageInvalidArgument)
			return
		}
		if _, err := sourceFile.Seek(start, io.SeekStart); err != nil {
			h.logError("seek source file for copy part range", err, source.bucketName, source.key, sourceObject.StoragePath)
			WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
			return
		}
		srcReader = io.LimitReader(sourceFile, end-start+1)
		srcSize = end - start + 1
	}

	// Acquire upload lock.
	release := h.acquireUploadLock(uploadID)
	defer release()

	// Re-read upload after locking.
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

	// Compute the ETag directly from the copy source data.
	// UploadPartCopy has no request body, so Content-MD5/payload checksum
	// headers from the SDK (computed against the empty body) must not be
	// validated against the source data — skip checksum pipeline entirely.
	md5Hash := md5.New()
	copyReader := io.TeeReader(srcReader, md5Hash)

	storagePath, size, err := h.Storage.WritePart(r.Context(), uploadID, partNumber, copyReader)
	if err != nil {
		h.writeStorageMutationError(w, r, err, bucketName, key, "")
		return
	}

	etag := hex.EncodeToString(md5Hash.Sum(nil))

	part := &metadata.MultipartPart{
		UploadID:    uploadID,
		PartNumber:  partNumber,
		Size:        size,
		ETag:        etag,
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

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(CopyPartResult{
		LastModified: part.CreatedAt.Format(time.RFC3339),
		ETag:         quoteETag(part.ETag),
	})
}

// parseByteRange parses an x-amz-copy-source-range value in the format "bytes=start-end"
// and clamps to the object size.
func parseByteRange(rangeHeader string, objectSize int64) (start, end int64, err error) {
	const prefix = "bytes="
	if !strings.HasPrefix(rangeHeader, prefix) {
		return 0, 0, fmt.Errorf("invalid byte range format")
	}
	rangeVal := strings.TrimSpace(rangeHeader[len(prefix):])
	parts := strings.SplitN(rangeVal, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid byte range format")
	}
	start, err = strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || start < 0 {
		return 0, 0, fmt.Errorf("invalid byte range start")
	}
	end, err = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil || end < 0 {
		return 0, 0, fmt.Errorf("invalid byte range end")
	}
	if start > end {
		return 0, 0, fmt.Errorf("invalid byte range")
	}
	if start >= objectSize {
		return 0, 0, fmt.Errorf("range start exceeds object size")
	}
	if end >= objectSize {
		return start, end, fmt.Errorf("%w: range end %d >= object size %d", errRangeExceedsSize, end, objectSize)
	}
	return start, end, nil
}

// DispatchPut routes PUT requests to either PutObject or UploadPart.
func (h *ObjectHandlers) DispatchPut(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hasUploadID := q.Has("uploadId")
	hasPartNumber := q.Has("partNumber")
	if hasUploadID && hasPartNumber && q.Get("uploadId") != "" && q.Get("partNumber") != "" {
		if r.Header.Get("x-amz-copy-source") != "" {
			h.UploadPartCopy(w, r)
			return
		}
		h.UploadPart(w, r)
		return
	}
	if hasUploadID || hasPartNumber {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if r.Header.Get("x-amz-copy-source") != "" {
		h.CopyObject(w, r)
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
