package s3

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/i-got-this-faa/fbs/internal/iam"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

func (h *ObjectHandlers) loadObjectForRead(w http.ResponseWriter, r *http.Request, bucketName, key string) (*metadata.Object, bool) {
	if key == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return nil, false
	}
	if !h.ensureBucketAction(w, r, bucketName, iam.ActionGetObject, key, "") {
		return nil, false
	}

	obj, err := h.Objects.GetByKey(r.Context(), bucketName, key)
	if errors.Is(err, metadata.ErrObjectNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchKey, messageNoSuchKey)
		return nil, false
	}
	if err != nil {
		h.logError("load object metadata", err, bucketName, key, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return nil, false
	}

	return obj, true
}

type storageReadErrorMapper func(http.ResponseWriter, *http.Request, *ObjectHandlers, error, *metadata.Object)

func (h *ObjectHandlers) serveObject(w http.ResponseWriter, r *http.Request, obj *metadata.Object, cacheControl, responseContentDisposition string, mapReadError storageReadErrorMapper) {
	file, err := h.Storage.Open(r.Context(), obj.StoragePath)
	if err != nil {
		mapReadError(w, r, h, err, obj)
		return
	}
	defer file.Close()

	setObjectHeaders(w, obj, cacheControl, responseContentDisposition)
	http.ServeContent(w, r, obj.Key, obj.UpdatedAt.UTC(), file)
}

func setObjectHeaders(w http.ResponseWriter, obj *metadata.Object, cacheControl, responseContentDisposition string) {
	w.Header().Set("ETag", quoteETag(obj.ETag))
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("Last-Modified", obj.UpdatedAt.UTC().Format(http.TimeFormat))
	if obj.ContentType != "" {
		w.Header().Set("Content-Type", obj.ContentType)
	}
	if responseContentDisposition != "" {
		w.Header().Set("Content-Disposition", responseContentDisposition)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if cacheControl != "" {
		w.Header().Set("Cache-Control", cacheControl)
	}
	if obj.IsMultipart && obj.PartsCount > 0 {
		w.Header().Set("x-amz-mp-parts-count", strconv.Itoa(obj.PartsCount))
	}
	for key, val := range obj.UserMetadata {
		// Use direct map write to preserve lowercase x-amz-meta-* header name.
		// Go's Header.Set() would canonicalize "x-amz-meta-foo" to "X-Amz-Meta-Foo",
		// which causes botocore to read the metadata key as "Foo" instead of "foo".
		w.Header()["x-amz-meta-"+key] = []string{val}
	}
}

const responseContentDispositionQuery = "response-content-disposition"

func parseResponseContentDisposition(values []string, present bool) (string, bool) {
	if !present {
		return "", true
	}
	if len(values) != 1 || values[0] == "" {
		return "", false
	}
	if _, _, err := mime.ParseMediaType(values[0]); err != nil {
		return "", false
	}
	return values[0], true
}

func requestedResponseContentDisposition(w http.ResponseWriter, r *http.Request) (string, bool) {
	values, present := r.URL.Query()[responseContentDispositionQuery]
	disposition, ok := parseResponseContentDisposition(values, present)
	if !ok {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidArgument, messageInvalidArgument)
		return "", false
	}
	return disposition, true
}

func mapAuthenticatedStorageReadError(w http.ResponseWriter, r *http.Request, h *ObjectHandlers, err error, obj *metadata.Object) {
	if errors.Is(err, storage.ErrNotFound) {
		h.logError("object metadata exists but backing file is missing", err, obj.BucketName, obj.Key, obj.StoragePath)
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	h.logError("open object backing file", err, obj.BucketName, obj.Key, obj.StoragePath)
	WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
}

func (h *ObjectHandlers) PublicReadObject(w http.ResponseWriter, r *http.Request) {
	responseContentDisposition, ok := h.validatePublicReadSignature(w, r)
	if !ok {
		return
	}

	bucketName, key := objectRouteParams(r)
	if bucketName == "" || key == "" {
		writePublicReadError(w, http.StatusNotFound)
		return
	}

	obj, err := h.Objects.GetByKey(r.Context(), bucketName, key)
	if errors.Is(err, metadata.ErrObjectNotFound) {
		writePublicReadError(w, http.StatusNotFound)
		return
	}
	if err != nil {
		h.logError("load public object metadata", err, bucketName, key, "")
		writePublicReadError(w, http.StatusInternalServerError)
		return
	}

	h.serveObject(w, r, obj, h.publicCacheControl(r), responseContentDisposition, mapPublicStorageReadError)
}

func (h *ObjectHandlers) validatePublicReadSignature(w http.ResponseWriter, r *http.Request) (string, bool) {
	query := r.URL.Query()
	dispositionValues, hasDisposition := query[responseContentDispositionQuery]
	responseContentDisposition, validDisposition := parseResponseContentDisposition(dispositionValues, hasDisposition)
	requiredQueryValues := 2
	if hasDisposition {
		requiredQueryValues++
	}
	if h.PublicReadSigner == nil ||
		len(query) != requiredQueryValues ||
		len(query["expires"]) != 1 ||
		len(query["signature"]) != 1 ||
		!validDisposition {
		writePublicReadError(w, http.StatusForbidden)
		return "", false
	}

	expires := query.Get("expires")
	signature := query.Get("signature")
	if err := h.PublicReadSigner.Verify(r.URL.EscapedPath(), expires, responseContentDisposition, signature); err != nil {
		writePublicReadError(w, http.StatusForbidden)
		return "", false
	}

	return responseContentDisposition, true
}

func (h *ObjectHandlers) publicCacheControl(r *http.Request) string {
	expiresUnix, err := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	if err != nil {
		return "public, max-age=0, must-revalidate"
	}

	remaining := time.Unix(expiresUnix, 0).Sub(h.now())
	if remaining <= 0 {
		return "public, max-age=0, must-revalidate"
	}

	maxAge := int64(remaining.Truncate(time.Second).Seconds())
	return fmt.Sprintf("public, max-age=%d, must-revalidate", maxAge)
}

func mapPublicStorageReadError(w http.ResponseWriter, r *http.Request, h *ObjectHandlers, err error, obj *metadata.Object) {
	if errors.Is(err, storage.ErrNotFound) {
		h.logError("public object metadata exists but backing file is missing", err, obj.BucketName, obj.Key, obj.StoragePath)
		writePublicReadError(w, http.StatusInternalServerError)
		return
	}

	h.logError("open public object backing file", err, obj.BucketName, obj.Key, obj.StoragePath)
	writePublicReadError(w, http.StatusInternalServerError)
}

func writePublicReadError(w http.ResponseWriter, statusCode int) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, http.StatusText(statusCode), statusCode)
}
