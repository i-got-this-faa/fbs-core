package s3

import (
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

type copyObjectResult struct {
	XMLName      xml.Name `xml:"CopyObjectResult"`
	LastModified string   `xml:"LastModified"`
	ETag         string   `xml:"ETag"`
}

type copySource struct {
	bucketName string
	key        string
	versionID  string
}

func (h *ObjectHandlers) CopyObject(w http.ResponseWriter, r *http.Request) {
	destinationBucket, destinationKey := objectRouteParams(r)
	if destinationKey == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if !requireSignedHeader(w, r, "x-amz-copy-source") {
		return
	}
	if r.Header.Get("x-amz-metadata-directive") != "" && !requireSignedHeader(w, r, "x-amz-metadata-directive") {
		return
	}

	source, err := parseCopySource(r.Header.Get("x-amz-copy-source"))
	if err != nil {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if source.versionID != "" {
		h.NotImplemented(w, r)
		return
	}

	// Copy requires get on source and put on destination (both checks always).
	if !h.ensureBucketAction(w, r, source.bucketName, authz.ActionGetObject, source.key, "") {
		return
	}
	if !h.ensureBucketAction(w, r, destinationBucket, authz.ActionPutObject, destinationKey, "") {
		return
	}

	sourceObject, err := h.Objects.GetByKey(r.Context(), source.bucketName, source.key)
	if errors.Is(err, metadata.ErrObjectNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchKey, messageNoSuchKey)
		return
	}
	if err != nil {
		h.logError("load source object", err, source.bucketName, source.key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	oldDestinationObject, err := h.Objects.GetByKey(r.Context(), destinationBucket, destinationKey)
	if err != nil && !errors.Is(err, metadata.ErrObjectNotFound) {
		h.logError("load existing destination object before copy", err, destinationBucket, destinationKey, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	sourceFile, err := h.Storage.Open(r.Context(), sourceObject.StoragePath)
	if err != nil {
		mapAuthenticatedStorageReadError(w, r, h, err, sourceObject)
		return
	}
	defer sourceFile.Close()

	contentType, ok := copyObjectContentType(r, sourceObject.ContentType)
	if !ok {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	storagePath, size, err := h.Storage.Write(r.Context(), destinationBucket, destinationKey, sourceFile)
	if err != nil {
		h.writeStorageMutationError(w, r, err, destinationBucket, destinationKey, "")
		return
	}

	now := h.now()

	// Determine metadata: COPY inherits from source, REPLACE parses from request.
	var userMeta map[string]string
	directive := strings.ToUpper(strings.TrimSpace(r.Header.Get("x-amz-metadata-directive")))
	switch directive {
	case "", "COPY":
		userMeta = sourceObject.UserMetadata
	case "REPLACE":
		userMeta = parseMetadataHeaders(r)
	}

	destinationObject := &metadata.Object{
		ID:           h.newID(),
		BucketName:   destinationBucket,
		Key:          destinationKey,
		Size:         size,
		ETag:         sourceObject.ETag,
		ContentType:  contentType,
		StoragePath:  storagePath,
		CreatedAt:    now,
		UpdatedAt:    now,
		UserMetadata: userMeta,
	}
	if err := h.Objects.Create(r.Context(), destinationObject); err != nil {
		h.logError("create copied object metadata", err, destinationBucket, destinationKey, storagePath)
		_ = h.Storage.Delete(r.Context(), storagePath)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	h.recordActivity(r, "copy_object", destinationBucket, destinationKey, size, destinationObject.ETag)

	if oldDestinationObject != nil && oldDestinationObject.StoragePath != storagePath {
		if err := h.Storage.Delete(r.Context(), oldDestinationObject.StoragePath); err != nil {
			h.logError("delete old object backing file after copy", err, destinationBucket, destinationKey, oldDestinationObject.StoragePath)
		}
	}

	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("ETag", quoteETag(destinationObject.ETag))
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(copyObjectResult{
		LastModified: now.Format("2006-01-02T15:04:05.000Z"),
		ETag:         quoteETag(destinationObject.ETag),
	})
}

func parseCopySource(value string) (copySource, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(value), "/")
	if trimmed == "" {
		return copySource{}, errors.New("missing copy source")
	}

	pathPart, rawQuery, _ := strings.Cut(trimmed, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return copySource{}, err
	}

	bucketPart, keyPart, ok := strings.Cut(pathPart, "/")
	if !ok || bucketPart == "" || keyPart == "" {
		return copySource{}, errors.New("invalid copy source")
	}

	bucketName, err := url.PathUnescape(bucketPart)
	if err != nil {
		return copySource{}, err
	}
	key, err := url.PathUnescape(keyPart)
	if err != nil {
		return copySource{}, err
	}

	return copySource{
		bucketName: bucketName,
		key:        key,
		versionID:  query.Get("versionId"),
	}, nil
}

func copyObjectContentType(r *http.Request, sourceContentType string) (string, bool) {
	directive := strings.ToUpper(strings.TrimSpace(r.Header.Get("x-amz-metadata-directive")))
	switch directive {
	case "", "COPY":
		return sourceContentType, true
	case "REPLACE":
		contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		return contentType, true
	default:
		return "", false
	}
}
