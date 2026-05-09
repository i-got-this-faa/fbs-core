package s3

import (
	"errors"
	"net/http"
	"strings"

	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func (h *ObjectHandlers) loadBucketForAccess(w http.ResponseWriter, r *http.Request, bucketName string) (*metadata.Bucket, bool) {
	if strings.TrimSpace(bucketName) == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return nil, false
	}

	bucket, err := h.Buckets.GetByName(r.Context(), bucketName)
	if errors.Is(err, metadata.ErrBucketNotFound) {
		WriteS3Error(w, r, http.StatusNotFound, codeNoSuchBucket, messageNoSuchBucket)
		return nil, false
	}
	if err != nil {
		h.logError("load bucket", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return nil, false
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || !principalCanAccessBucket(principal, bucket) {
		WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
		return nil, false
	}

	return bucket, true
}

func principalCanAccessBucket(principal auth.Principal, bucket *metadata.Bucket) bool {
	if bucket == nil || strings.TrimSpace(principal.UserID) == "" {
		return false
	}
	return principal.Role == "admin" || bucket.OwnerID == principal.UserID
}

func (h *ObjectHandlers) ensureBucket(w http.ResponseWriter, r *http.Request, bucketName string) bool {
	_, ok := h.loadBucketForAccess(w, r, bucketName)
	return ok
}

func requireSignedHeader(w http.ResponseWriter, r *http.Request, headerName string) bool {
	if requestHasSignedHeader(r, headerName) {
		return true
	}

	WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
	return false
}

func requestHasSignedHeader(r *http.Request, headerName string) bool {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || strings.TrimSpace(principal.SignedHeaders) == "" {
		return true
	}

	needle := strings.ToLower(headerName)
	for _, signedHeader := range strings.Split(principal.SignedHeaders, ";") {
		if strings.EqualFold(strings.TrimSpace(signedHeader), needle) {
			return true
		}
	}
	return false
}
