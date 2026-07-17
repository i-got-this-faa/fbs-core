package s3

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"time"

	"github.com/i-got-this-faa/fbs/internal/authz"
)

// ListObjectVersions returns current objects as versions (versioning not supported).
func (h *ObjectHandlers) ListObjectVersions(w http.ResponseWriter, r *http.Request) {
	bucketName := chiBucketParam(r)
	prefix := r.URL.Query().Get("prefix")
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionListBucket, "", prefix) {
		return
	}

	maxKeys := 1000
	if v := r.URL.Query().Get("max-keys"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed <= 0 {
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidArgument, messageInvalidArgument)
			return
		}
		maxKeys = parsed
		if maxKeys > 1000 {
			maxKeys = 1000
		}
	}

	keyMarker := r.URL.Query().Get("key-marker")

	objects, isTruncated, err := h.Objects.List(r.Context(), bucketName, prefix, keyMarker, maxKeys)
	if err != nil {
		h.logError("list objects for versions", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	versions := make([]ListVersionsEntry, 0, len(objects))
	for _, obj := range objects {
		versions = append(versions, ListVersionsEntry{
			Key:          obj.Key,
			VersionID:    "null",
			IsLatest:     true,
			LastModified: obj.UpdatedAt.Format(time.RFC3339),
			ETag:         quoteETag(obj.ETag),
			Size:         obj.Size,
			StorageClass: "STANDARD",
		})
	}

	nextKeyMarker := ""
	if isTruncated && len(objects) > 0 {
		nextKeyMarker = objects[len(objects)-1].Key
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(ListVersionsResult{
		Xmlns:         "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:          bucketName,
		Prefix:        prefix,
		KeyMarker:     keyMarker,
		NextKeyMarker: nextKeyMarker,
		MaxKeys:       maxKeys,
		IsTruncated:   isTruncated,
		Version:       versions,
	})
}
