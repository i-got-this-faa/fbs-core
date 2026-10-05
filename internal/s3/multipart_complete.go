package s3

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"

	"github.com/i-got-this-faa/fbs/internal/iam"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

var (
	errNoCompletedParts = errors.New("no parts in complete request")
	errPartOrder        = errors.New("parts are not in ascending order")
	errUnknownPart      = errors.New("part is missing or its ETag does not match")
	errPartTooSmall     = errors.New("non-final part is smaller than the minimum part size")
)

// CompleteMultipartUpload handles POST /{bucket}/{key}?uploadId={id}.
func (h *ObjectHandlers) CompleteMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucketName, key, uploadID, ok := h.multipartTarget(w, r, iam.ActionPutObject)
	if !ok {
		return
	}
	if _, ok := h.loadUpload(w, r, uploadID, bucketName, key); !ok {
		return
	}
	upload, release, ok := h.lockUpload(w, r, uploadID, bucketName, key)
	if !ok {
		return
	}
	defer release()

	claim, ok := h.claimUpload(w, r, uploadID, metadata.MultipartUploadStatusCompleting, bucketName, key)
	if !ok {
		return
	}
	defer claim.release()

	var req CompleteMultipartUpload
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteS3Error(w, r, http.StatusBadRequest, codeMalformedXML, messageMalformedXML)
		return
	}
	requested, err := normalizeCompletedParts(req.Parts)
	if err != nil {
		writeCompletedPartsError(w, r, err)
		return
	}
	storedParts, err := h.MultipartUploads.ListParts(r.Context(), uploadID)
	if err != nil {
		h.logError("list multipart parts", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	parts, err := matchStoredParts(requested, storedParts, h.minPartSize())
	if err != nil {
		writeCompletedPartsError(w, r, err)
		return
	}

	obj, ok := h.assembleUpload(w, r, upload, parts)
	if !ok {
		return
	}

	// CompleteUpload atomically verifies the claim, writes the object, removes
	// the upload, and returns the storage path of the object it replaced.
	replacedPath, err := h.MultipartUploads.CompleteUpload(r.Context(), obj, uploadID, r.Header.Get("If-Match"), r.Header.Get("If-None-Match"))
	if err != nil {
		h.deleteStoredFile(obj.StoragePath, "discard assembled object after failed completion", bucketName, key)
		h.writeCompleteUploadError(w, r, err, claim, obj.StoragePath)
		return
	}
	claim.settle()

	metadata.PutObjectInCache(h.Objects, obj)
	h.recordActivity(r, metadata.ActivityCompleteMultipartUpload, bucketName, key, obj.Size, obj.ETag)
	if replacedPath != "" && replacedPath != obj.StoragePath {
		h.deleteStoredFile(replacedPath, "delete old object backing file after multipart complete", bucketName, key)
	}
	h.deleteUploadParts(uploadID, bucketName, key)

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(CompleteMultipartUploadResult{
		Location: fmt.Sprintf("/%s/%s", bucketName, key),
		Bucket:   bucketName,
		Key:      key,
		ETag:     quoteETag(obj.ETag),
		Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
	})
}

// assembleUpload concatenates the parts into a new backing file and returns
// the object to commit. The file is removed if the object cannot be built.
func (h *ObjectHandlers) assembleUpload(w http.ResponseWriter, r *http.Request, upload *metadata.MultipartUpload, parts []metadata.MultipartPart) (*metadata.Object, bool) {
	partPaths := make([]string, 0, len(parts))
	partETags := make([]string, 0, len(parts))
	for _, part := range parts {
		partPaths = append(partPaths, part.StoragePath)
		partETags = append(partETags, part.ETag)
	}

	storagePath, size, err := h.Storage.AssembleParts(r.Context(), upload.BucketName, upload.Key, partPaths)
	if err != nil {
		h.writeStorageMutationError(w, r, err, upload.BucketName, upload.Key, "")
		return nil, false
	}
	etag, err := MultipartETag(partETags)
	if err != nil {
		h.logError("compute multipart etag", err, upload.BucketName, upload.Key, storagePath)
		h.deleteStoredFile(storagePath, "discard assembled object after etag failure", upload.BucketName, upload.Key)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return nil, false
	}

	contentType := upload.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	now := h.now()
	return &metadata.Object{
		ID:           h.newID(),
		BucketName:   upload.BucketName,
		Key:          upload.Key,
		Size:         size,
		ETag:         etag,
		ContentType:  contentType,
		StoragePath:  storagePath,
		CreatedAt:    now,
		UpdatedAt:    now,
		IsMultipart:  true,
		PartsCount:   len(parts),
		UserMetadata: upload.UserMetadata,
	}, true
}

func (h *ObjectHandlers) minPartSize() int64 {
	if h.MinPartSize == 0 {
		return defaultMinPartSize
	}
	return h.MinPartSize
}

// normalizeCompletedParts applies S3's rule that the last entry for a part
// number wins (at the position of its first entry), then requires ascending
// part numbers.
func normalizeCompletedParts(parts []CompletePart) ([]CompletePart, error) {
	if len(parts) == 0 {
		return nil, errNoCompletedParts
	}
	positions := make(map[int]int, len(parts))
	unique := make([]CompletePart, 0, len(parts))
	for _, part := range parts {
		if i, seen := positions[part.PartNumber]; seen {
			unique[i] = part
			continue
		}
		positions[part.PartNumber] = len(unique)
		unique = append(unique, part)
	}
	for i := 1; i < len(unique); i++ {
		if unique[i].PartNumber < unique[i-1].PartNumber {
			return nil, errPartOrder
		}
	}
	return unique, nil
}

// matchStoredParts returns the stored part for each requested part. Each
// requested ETag must match, and every part except the last must be at least
// minPartSize bytes.
func matchStoredParts(requested []CompletePart, stored []metadata.MultipartPart, minPartSize int64) ([]metadata.MultipartPart, error) {
	byNumber := make(map[int]metadata.MultipartPart, len(stored))
	for _, part := range stored {
		byNumber[part.PartNumber] = part
	}

	matched := make([]metadata.MultipartPart, 0, len(requested))
	for i, want := range requested {
		part, ok := byNumber[want.PartNumber]
		if !ok || !etagMatches(want.ETag, part.ETag) {
			return nil, errUnknownPart
		}
		if i < len(requested)-1 && part.Size < minPartSize {
			return nil, errPartTooSmall
		}
		matched = append(matched, part)
	}
	return matched, nil
}

func writeCompletedPartsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errPartOrder):
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidPartOrder, messageInvalidPartOrder)
	case errors.Is(err, errUnknownPart):
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidPart, messageInvalidPart)
	case errors.Is(err, errPartTooSmall):
		WriteS3Error(w, r, http.StatusBadRequest, codeEntityTooSmall, messageEntityTooSmall)
	default:
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
	}
}

// writeCompleteUploadError maps a CompleteUpload failure to an S3 error and
// decides whether the claim is released back to active.
func (h *ObjectHandlers) writeCompleteUploadError(w http.ResponseWriter, r *http.Request, err error, claim *uploadClaim, storagePath string) {
	switch {
	case errors.Is(err, metadata.ErrMultipartUploadNotFound):
		claim.settle()
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
	case errors.Is(err, metadata.ErrUploadAlreadyClaimed):
		// Another request completed or aborted the upload while this one
		// assembled it. Releasing restores the claim this request held.
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchUpload, messageNoSuchUpload)
	case errors.Is(err, metadata.ErrPreconditionFailed):
		// The transaction rolled back; deferred release keeps the upload usable.
		WriteS3Error(w, r, http.StatusPreconditionFailed, codePreconditionFailed, messagePreconditionFailed)
	case errors.Is(err, metadata.ErrObjectNotFound):
		// If-Match was set but the object does not exist.
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchKey, messageNoSuchKey)
	default:
		h.logError("complete multipart upload", err, claim.bucketName, claim.key, storagePath)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
	}
}
