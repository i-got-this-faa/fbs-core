package s3

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/i-got-this-faa/fbs/internal/iam"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

// UploadPart handles PUT /{bucket}/{key}?partNumber={n}&uploadId={id}.
func (h *ObjectHandlers) UploadPart(w http.ResponseWriter, r *http.Request) {
	bucketName, key, uploadID, ok := h.multipartTarget(w, r, iam.ActionPutObject)
	if !ok {
		return
	}
	partNumber, ok := parsePartNumber(w, r)
	if !ok {
		return
	}
	if _, ok := h.loadUpload(w, r, uploadID, bucketName, key); !ok {
		return
	}

	// Check conditional headers (If-Match / If-None-Match) against the existing object.
	existingObj, err := h.Objects.GetByKey(r.Context(), bucketName, key)
	if err != nil && !errors.Is(err, metadata.ErrObjectNotFound) {
		h.logError("get existing object for precondition check", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	if h.checkPreconditionFailed(w, r, existingObj) {
		return
	}

	upload, release, ok := h.lockUpload(w, r, uploadID, bucketName, key)
	if !ok {
		return
	}
	defer release()
	if upload.Status != metadata.MultipartUploadStatusActive {
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
		h.deleteStoredFile(storagePath, "discard part file with bad digest", bucketName, key)
		WriteS3Error(w, r, http.StatusBadRequest, codeBadDigest, messageBadDigest)
		return
	}

	checksums := pipeline.Checksums()
	part := &metadata.MultipartPart{
		UploadID:          uploadID,
		PartNumber:        partNumber,
		Size:              size,
		ETag:              pipeline.ETag(),
		StoragePath:       storagePath,
		CreatedAt:         h.now(),
		ChecksumCRC32:     checksums["x-amz-checksum-crc32"],
		ChecksumCRC32C:    checksums["x-amz-checksum-crc32c"],
		ChecksumCRC64NVME: checksums["x-amz-checksum-crc64nvme"],
		ChecksumSHA1:      checksums["x-amz-checksum-sha1"],
		ChecksumSHA256:    checksums["x-amz-checksum-sha256"],
	}
	if !h.storePart(w, r, part, bucketName, key) {
		return
	}

	w.Header().Set("ETag", quoteETag(part.ETag))
	for _, header := range partChecksumHeaders {
		if value := checksums[header]; value != "" {
			w.Header().Set(header, value)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// partChecksumHeaders are the additional checksums echoed on UploadPart. The
// pipeline also tracks Content-MD5 and the payload hash, which are not echoed.
var partChecksumHeaders = []string{
	"x-amz-checksum-crc32",
	"x-amz-checksum-crc32c",
	"x-amz-checksum-crc64nvme",
	"x-amz-checksum-sha1",
	"x-amz-checksum-sha256",
}

// UploadPartCopy handles PUT /{bucket}/{key}?partNumber={n}&uploadId={id} with x-amz-copy-source.
func (h *ObjectHandlers) UploadPartCopy(w http.ResponseWriter, r *http.Request) {
	bucketName, key, uploadID, ok := h.multipartTarget(w, r, iam.ActionPutObject)
	if !ok {
		return
	}
	partNumber, ok := parsePartNumber(w, r)
	if !ok {
		return
	}
	source, ok := parsePartCopySource(w, r)
	if !ok {
		return
	}
	if _, ok := h.loadUpload(w, r, uploadID, bucketName, key); !ok {
		return
	}
	if !h.ensureBucketAction(w, r, source.bucketName, iam.ActionGetObject, source.key, "") {
		return
	}

	sourceReader, closeSource, ok := h.openCopySource(w, r, source)
	if !ok {
		return
	}
	defer closeSource()

	_, release, ok := h.lockUpload(w, r, uploadID, bucketName, key)
	if !ok {
		return
	}
	defer release()

	// UploadPartCopy has no request body, so Content-MD5 and payload checksum
	// headers (computed by SDKs over the empty body) must not be checked
	// against the source data. The ETag is the MD5 of the copied bytes.
	md5Hash := md5.New()
	storagePath, size, err := h.Storage.WritePart(r.Context(), uploadID, partNumber, io.TeeReader(sourceReader, md5Hash))
	if err != nil {
		h.writeStorageMutationError(w, r, err, bucketName, key, "")
		return
	}

	part := &metadata.MultipartPart{
		UploadID:    uploadID,
		PartNumber:  partNumber,
		Size:        size,
		ETag:        hex.EncodeToString(md5Hash.Sum(nil)),
		StoragePath: storagePath,
		CreatedAt:   h.now(),
	}
	if !h.storePart(w, r, part, bucketName, key) {
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(CopyPartResult{
		LastModified: part.CreatedAt.Format(time.RFC3339),
		ETag:         quoteETag(part.ETag),
	})
}

// parsePartCopySource reads and validates the x-amz-copy-source headers.
func parsePartCopySource(w http.ResponseWriter, r *http.Request) (copySource, bool) {
	header := r.Header.Get("x-amz-copy-source")
	if header == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return copySource{}, false
	}
	if !requireSignedHeader(w, r, "x-amz-copy-source") {
		return copySource{}, false
	}
	source, err := parseCopySource(header)
	if err != nil {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return copySource{}, false
	}
	if source.versionID != "" {
		WriteS3Error(w, r, http.StatusNotImplemented, codeNotImplemented, messageNotImplemented)
		return copySource{}, false
	}
	if r.Header.Get("x-amz-copy-source-range") != "" && !requireSignedHeader(w, r, "x-amz-copy-source-range") {
		return copySource{}, false
	}
	return source, true
}

// openCopySource opens the source object, limited to x-amz-copy-source-range
// when the header is set. When ok is true the caller must call closeSource.
func (h *ObjectHandlers) openCopySource(w http.ResponseWriter, r *http.Request, source copySource) (reader io.Reader, closeSource func(), ok bool) {
	sourceObject, err := h.Objects.GetByKey(r.Context(), source.bucketName, source.key)
	if errors.Is(err, metadata.ErrObjectNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchKey, messageNoSuchKey)
		return nil, nil, false
	}
	if err != nil {
		h.logError("get source object for copy part", err, source.bucketName, source.key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return nil, nil, false
	}

	sourceFile, err := h.Storage.Open(r.Context(), sourceObject.StoragePath)
	if err != nil {
		h.logError("open source object for copy part", err, source.bucketName, source.key, sourceObject.StoragePath)
		if errors.Is(err, storage.ErrNotFound) {
			WriteS3Error(w, r, http.StatusNotFound, codeNoSuchKey, messageNoSuchKey)
			return nil, nil, false
		}
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return nil, nil, false
	}
	closeSource = func() { _ = sourceFile.Close() }

	rangeHeader := r.Header.Get("x-amz-copy-source-range")
	if rangeHeader == "" {
		return sourceFile, closeSource, true
	}
	start, end, err := parseByteRange(rangeHeader, sourceObject.Size)
	if err != nil {
		closeSource()
		if errors.Is(err, errRangeExceedsSize) {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", sourceObject.Size))
			WriteS3Error(w, r, http.StatusRequestedRangeNotSatisfiable, codeInvalidRange, "The requested range is not valid.")
			return nil, nil, false
		}
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidArgument, messageInvalidArgument)
		return nil, nil, false
	}
	if _, err := sourceFile.Seek(start, io.SeekStart); err != nil {
		closeSource()
		h.logError("seek source file for copy part range", err, source.bucketName, source.key, sourceObject.StoragePath)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return nil, nil, false
	}
	return io.LimitReader(sourceFile, end-start+1), closeSource, true
}

// parseByteRange parses an x-amz-copy-source-range value in the format "bytes=start-end".
// Returns a parse error (leading to a 416/400 response) for out-of-range values,
// not a clamped range.
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
