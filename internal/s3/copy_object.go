package s3

import (
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"strings"

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
	if !h.ensureBucket(w, r, destinationBucket) {
		return
	}
	if destinationKey == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
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

	if _, err := h.Buckets.GetByName(r.Context(), source.bucketName); errors.Is(err, metadata.ErrBucketNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchBucket, messageNoSuchBucket)
		return
	} else if err != nil {
		h.logError("load source bucket", err, source.bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
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

	sourceFile, err := h.Storage.Open(r.Context(), sourceObject.StoragePath)
	if err != nil {
		mapStorageReadError(w, r, h, err, sourceObject)
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
	destinationObject := &metadata.Object{
		ID:          h.newID(),
		BucketName:  destinationBucket,
		Key:         destinationKey,
		Size:        size,
		ETag:        sourceObject.ETag,
		ContentType: contentType,
		StoragePath: storagePath,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := h.Objects.Create(r.Context(), destinationObject); err != nil {
		h.logError("create copied object metadata", err, destinationBucket, destinationKey, storagePath)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	h.recordActivity(r, "copy_object", destinationBucket, destinationKey, size, destinationObject.ETag)

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
