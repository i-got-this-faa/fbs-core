package s3

import (
	"encoding/xml"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/publicread"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

type ObjectHandlers struct {
	Users            metadata.UserRepository
	Buckets          metadata.BucketRepository
	Objects          metadata.ObjectRepository
	Activity         metadata.ActivityRepository
	MultipartUploads metadata.MultipartUploadRepository
	Grants           metadata.GrantRepository
	Authz            *authz.Evaluator
	Storage          storage.DiskEngine
	Now              func() time.Time
	NewID            func() string
	Logger           *slog.Logger
	S3CacheControl   string
	PublicReadSigner *publicread.Signer
	MinPartSize      int64 // zero means default 5 MiB

	uploadLocksMu sync.Mutex
	uploadLocks   map[string]*uploadLock
}

// parseMetadataHeaders extracts x-amz-meta-* headers from the request.
// It returns the metadata with lowercased keys, matching AWS S3 conventions.
func parseMetadataHeaders(r *http.Request) map[string]string {
	meta := make(map[string]string)
	const prefix = "x-amz-meta-"
	for key, vals := range r.Header {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, prefix) {
			name := lower[len(prefix):]
			if len(vals) > 0 {
				meta[name] = vals[0]
			}
		}
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

type uploadLock struct {
	mu   sync.Mutex
	refs int
}

func (h *ObjectHandlers) acquireUploadLock(uploadID string) func() {
	h.uploadLocksMu.Lock()
	if h.uploadLocks == nil {
		h.uploadLocks = make(map[string]*uploadLock)
	}
	lock := h.uploadLocks[uploadID]
	if lock == nil {
		lock = &uploadLock{}
		h.uploadLocks[uploadID] = lock
	}
	lock.refs++
	h.uploadLocksMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()

		h.uploadLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 && h.uploadLocks[uploadID] == lock {
			delete(h.uploadLocks, uploadID)
		}
		h.uploadLocksMu.Unlock()
	}
}

func (h *ObjectHandlers) PutObject(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionPutObject, key, "") {
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

	// Load the existing object before writing so we know whether this is an
	// overwrite. Write always creates a new unique UUID path, so the old file
	// is untouched until metadata commit succeeds.
	oldObj, err := h.Objects.GetByKey(r.Context(), bucketName, key)
	if err != nil && !errors.Is(err, metadata.ErrObjectNotFound) {
		h.logError("load existing object before put", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	storagePath, size, err := h.Storage.Write(r.Context(), bucketName, key, pipeline.Reader(r.Body))
	if err != nil {
		h.writeStorageMutationError(w, r, err, bucketName, key, "")
		return
	}

	if err := pipeline.Validate(); err != nil {
		_ = h.Storage.Delete(r.Context(), storagePath)
		WriteS3Error(w, r, http.StatusBadRequest, codeBadDigest, messageBadDigest)
		return
	}

	now := h.now()
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	cs := pipeline.Checksums()
	obj := &metadata.Object{
		ID:                h.newID(),
		BucketName:        bucketName,
		Key:               key,
		Size:              size,
		ETag:              pipeline.ETag(),
		ContentType:       contentType,
		StoragePath:       storagePath,
		CreatedAt:         now,
		UpdatedAt:         now,
		UserMetadata:      parseMetadataHeaders(r),
		ChecksumCRC32:     cs["x-amz-checksum-crc32"],
		ChecksumCRC32C:    cs["x-amz-checksum-crc32c"],
		ChecksumCRC64NVME: cs["x-amz-checksum-crc64nvme"],
		ChecksumSHA1:      cs["x-amz-checksum-sha1"],
		ChecksumSHA256:    cs["x-amz-checksum-sha256"],
	}

	if err := h.Objects.Create(r.Context(), obj); err != nil {
		h.logError("create object metadata", err, bucketName, key, storagePath)
		// Write always creates a new unique file, so on metadata failure we must
		// clean it up regardless of whether this is an overwrite.
		_ = h.Storage.Delete(r.Context(), storagePath)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	h.recordActivity(r, "put_object", bucketName, key, size, obj.ETag)

	if oldObj != nil && oldObj.StoragePath != storagePath {
		if err := h.Storage.Delete(r.Context(), oldObj.StoragePath); err != nil {
			h.logError("delete old object backing file after put", err, bucketName, key, oldObj.StoragePath)
		}
	}

	w.Header().Set("ETag", quoteETag(obj.ETag))
	w.WriteHeader(http.StatusOK)
}
func (h *ObjectHandlers) GetObject(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	obj, ok := h.loadObjectForRead(w, r, bucketName, key)
	if !ok {
		return
	}

	if h.handlePartNumber(w, r, obj) {
		return
	}

	h.serveObject(w, r, obj, h.S3CacheControl, mapAuthenticatedStorageReadError)
}

func (h *ObjectHandlers) HeadObject(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	obj, ok := h.loadObjectForRead(w, r, bucketName, key)
	if !ok {
		return
	}

	if h.handlePartNumber(w, r, obj) {
		return
	}

	h.serveObject(w, r, obj, h.S3CacheControl, mapAuthenticatedStorageReadError)
}

// handlePartNumber handles the partNumber query parameter for GetObject/HeadObject.
// Returns true if the response was written (error or full part response).
func (h *ObjectHandlers) handlePartNumber(w http.ResponseWriter, r *http.Request, obj *metadata.Object) bool {
	partNumStr := r.URL.Query().Get("partNumber")
	if partNumStr == "" {
		return false
	}

	partNumber, err := strconv.Atoi(partNumStr)
	if err != nil || partNumber < 1 {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidArgument, messageInvalidArgument)
		return true
	}

	if !obj.IsMultipart {
		if partNumber != 1 {
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidPart, messageInvalidPart)
			return true
		}
		// PartNumber=1 on a non-multipart object returns the full object.
		// x-amz-mp-parts-count is only returned for multipart objects.
		return false
	}

	if partNumber > obj.PartsCount {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidPart, messageInvalidPart)
		return true
	}

	// Per-part range serving is not yet implemented; returning the full object
	// with a wrong Content-Length would silently corrupt parallel downloads.
	// Return 501 until per-part storage metadata is available.
	w.Header().Set("x-amz-mp-parts-count", strconv.Itoa(obj.PartsCount))
	WriteS3Error(w, r, http.StatusNotImplemented, codeNotImplemented, messageNotImplemented)
	return true
}
func (h *ObjectHandlers) GetObjectAttributes(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	obj, ok := h.loadObjectForRead(w, r, bucketName, key)
	if !ok {
		return
	}
	// AWS requires this header; default to all if not provided.
	attrHeader := r.Header.Get("x-amz-object-attributes")
	requested := make(map[string]bool)
	if attrHeader != "" {
		for _, attr := range strings.Split(attrHeader, ",") {
			requested[strings.TrimSpace(strings.ToLower(attr))] = true
		}
	} else {
		// If no header, return all attributes (broad compatibility).
		requested["etag"] = true
		requested["checksum"] = true
		requested["objectparts"] = true
		requested["storageclass"] = true
	}

	var objectParts *ObjectPartsInfo
	if obj.IsMultipart && requested["objectparts"] {
		objectParts = &ObjectPartsInfo{PartsCount: obj.PartsCount}
	}

	var checksum *ObjectChecksum
	if requested["checksum"] && (obj.ChecksumCRC32 != "" || obj.ChecksumCRC32C != "" || obj.ChecksumCRC64NVME != "" || obj.ChecksumSHA1 != "" || obj.ChecksumSHA256 != "") {
		checksum = &ObjectChecksum{
			ChecksumCRC32:     obj.ChecksumCRC32,
			ChecksumCRC32C:    obj.ChecksumCRC32C,
			ChecksumCRC64NVME: obj.ChecksumCRC64NVME,
			ChecksumSHA1:      obj.ChecksumSHA1,
			ChecksumSHA256:    obj.ChecksumSHA256,
		}
	}

	result := GetObjectAttributesResult{
		Xmlns:        "http://s3.amazonaws.com/doc/2006-03-01/",
		LastModified: obj.UpdatedAt.Format(time.RFC3339),
		ObjectSize:   obj.Size,
	}
	if requested["etag"] {
		result.ETag = quoteETag(obj.ETag)
	}
	if requested["storageclass"] {
		result.StorageClass = "STANDARD"
	}
	result.ObjectParts = objectParts
	result.Checksum = checksum

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(result)
}

func (h *ObjectHandlers) DeleteObject(w http.ResponseWriter, r *http.Request) {
	bucketName, key := objectRouteParams(r)
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionDeleteObject, key, "") {
		return
	}

	obj, existed, err := h.deleteObject(r, bucketName, key)
	if err != nil {
		h.logError("delete object", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	if !existed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.recordActivity(r, "delete_object", bucketName, key, obj.Size, obj.ETag)

	w.WriteHeader(http.StatusNoContent)
}

func (h *ObjectHandlers) writeStorageMutationError(w http.ResponseWriter, r *http.Request, err error, bucketName, key, storagePath string) {
	if errors.Is(err, storage.ErrInvalidKey) || errors.Is(err, storage.ErrPathTraversal) {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	h.logError("mutate object backing file", err, bucketName, key, storagePath)
	WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
}

// checkPreconditionFailed returns true and writes an S3 error if the
// conditional headers (If-Match / If-None-Match) reject the request.
func (h *ObjectHandlers) checkPreconditionFailed(w http.ResponseWriter, r *http.Request, obj *metadata.Object) bool {
	ifMatch := r.Header.Get("If-Match")
	ifNoneMatch := r.Header.Get("If-None-Match")
	if ifMatch == "" && ifNoneMatch == "" {
		return false
	}

	if obj == nil && ifMatch != "" {
		// S3 returns NoSuchKey (not PreconditionFailed) when If-Match is
		// specified and the object does not exist. Let the caller handle 404.
		return false
	}

	if obj != nil {
		if ifMatch != "" && ifMatch != "*" && !strings.EqualFold(unquoteETag(ifMatch), unquoteETag(obj.ETag)) {
			WriteS3Error(w, r, http.StatusPreconditionFailed, codePreconditionFailed, messagePreconditionFailed)
			return true
		}
		if ifNoneMatch == "*" {
			WriteS3Error(w, r, http.StatusPreconditionFailed, codePreconditionFailed, messagePreconditionFailed)
			return true
		}
		if ifNoneMatch != "" && ifNoneMatch != "*" && strings.EqualFold(unquoteETag(ifNoneMatch), unquoteETag(obj.ETag)) {
			WriteS3Error(w, r, http.StatusPreconditionFailed, codePreconditionFailed, messagePreconditionFailed)
			return true
		}
	}

	return false
}

func (h *ObjectHandlers) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func (h *ObjectHandlers) newID() string {
	if h.NewID != nil {
		return h.NewID()
	}
	return uuid.NewString()
}

func (h *ObjectHandlers) logError(message string, err error, bucketName, key, storagePath string) {
	if h.Logger == nil {
		return
	}
	h.Logger.Error(message, "error", err, "bucket", bucketName, "key", key, "storage_path", storagePath)
}

func objectRouteParams(r *http.Request) (string, string) {
	return chi.URLParam(r, "bucket"), strings.TrimPrefix(chi.URLParam(r, "*"), "/")
}

func quoteETag(etag string) string {
	trimmed := strings.TrimSpace(etag)
	if strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`) {
		return trimmed
	}
	return `"` + trimmed + `"`
}
