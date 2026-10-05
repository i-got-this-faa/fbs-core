package s3

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/i-got-this-faa/fbs/internal/iam"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

// ListMultipartUploads handles GET /{bucket}?uploads.
func (h *ObjectHandlers) ListMultipartUploads(w http.ResponseWriter, r *http.Request) {
	bucketName := chiBucketParam(r)
	prefix := r.URL.Query().Get("prefix")
	if !h.ensureBucketAction(w, r, bucketName, iam.ActionListBucket, "", prefix) {
		return
	}

	q := r.URL.Query()
	maxUploads, ok := parseListLimit(w, r, q.Get("max-uploads"))
	if !ok {
		return
	}

	prefix = q.Get("prefix")
	keyMarker := q.Get("key-marker")
	uploadIDMarker := q.Get("upload-id-marker")
	delimiter := q.Get("delimiter")
	if delimiter != "" {
		WriteS3Error(w, r, http.StatusNotImplemented, codeNotImplemented, "delimiter is not yet supported")
		return
	}

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

// ListParts handles GET /{bucket}/{key}?uploadId={id}.
func (h *ObjectHandlers) ListParts(w http.ResponseWriter, r *http.Request) {
	bucketName, key, uploadID, ok := h.multipartTarget(w, r, iam.ActionListMultipartUploadParts)
	if !ok {
		return
	}
	if _, ok := h.loadUpload(w, r, uploadID, bucketName, key); !ok {
		return
	}

	parts, err := h.MultipartUploads.ListParts(r.Context(), uploadID)
	if err != nil {
		h.logError("list parts", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	q := r.URL.Query()
	maxParts, ok := parseListLimit(w, r, q.Get("max-parts"))
	if !ok {
		return
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

	page, isTruncated := partsAfterMarker(parts, partNumberMarker, maxParts)
	partEntries := make([]ListPartsPart, 0, len(page))
	for _, p := range page {
		partEntries = append(partEntries, ListPartsPart{
			PartNumber:        p.PartNumber,
			LastModified:      p.CreatedAt.Format(time.RFC3339),
			ETag:              quoteETag(p.ETag),
			Size:              p.Size,
			ChecksumCRC32:     p.ChecksumCRC32,
			ChecksumCRC32C:    p.ChecksumCRC32C,
			ChecksumCRC64NVME: p.ChecksumCRC64NVME,
			ChecksumSHA1:      p.ChecksumSHA1,
			ChecksumSHA256:    p.ChecksumSHA256,
		})
	}

	nextMarker := 0
	if isTruncated && len(page) > 0 {
		nextMarker = page[len(page)-1].PartNumber
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

// partsAfterMarker returns up to limit parts numbered above marker. parts must
// be sorted by part number.
func partsAfterMarker(parts []metadata.MultipartPart, marker, limit int) ([]metadata.MultipartPart, bool) {
	start := len(parts)
	for i, p := range parts {
		if p.PartNumber > marker {
			start = i
			break
		}
	}
	page := parts[start:]
	if len(page) > limit {
		return page[:limit], true
	}
	return page, false
}
