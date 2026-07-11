package s3

import (
	"errors"
	"net/http"
	"strings"

	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

// authorize checks whether the request principal may perform action on the
// given bucket/key. On deny it writes AccessDenied; on evaluator failure it
// writes InternalError. Returns true only when access is allowed.
func (h *ObjectHandlers) authorize(w http.ResponseWriter, r *http.Request, action, bucketName, objectKey, listPrefix string, bucket *metadata.Bucket) bool {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || strings.TrimSpace(principal.UserID) == "" {
		WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
		return false
	}

	ownerID := ""
	if bucket != nil {
		ownerID = bucket.OwnerID
	}

	allowed, err := h.evaluator().Allow(r.Context(), authz.DecisionRequest{
		Principal:     principal,
		Action:        action,
		Bucket:        bucketName,
		ObjectKey:     objectKey,
		ListPrefix:    listPrefix,
		BucketOwnerID: ownerID,
	})
	if err != nil {
		h.logError("authorize", err, bucketName, objectKey, "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return false
	}
	if !allowed {
		WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
		return false
	}
	return true
}

// authorizeQuiet is like authorize but does not write an HTTP response.
// Used for multi-object delete per-key checks.
func (h *ObjectHandlers) authorizeQuiet(r *http.Request, action, bucketName, objectKey string, bucket *metadata.Bucket) (bool, error) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || strings.TrimSpace(principal.UserID) == "" {
		return false, nil
	}

	ownerID := ""
	if bucket != nil {
		ownerID = bucket.OwnerID
	}

	return h.evaluator().Allow(r.Context(), authz.DecisionRequest{
		Principal:     principal,
		Action:        action,
		Bucket:        bucketName,
		ObjectKey:     objectKey,
		BucketOwnerID: ownerID,
	})
}

// authorizeBucketRelationship allows the request only when the principal is an
// admin, the bucket owner, or holds at least one active grant on the bucket.
// Callers with no relationship to the bucket get AccessDenied (same top-level
// deny shape as other handlers), rather than a success-shaped multi-status body.
func (h *ObjectHandlers) authorizeBucketRelationship(w http.ResponseWriter, r *http.Request, bucket *metadata.Bucket) bool {
	if bucket == nil {
		WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
		return false
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || strings.TrimSpace(principal.UserID) == "" {
		WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
		return false
	}
	if principal.Role == "admin" || bucket.OwnerID == principal.UserID {
		return true
	}
	if h.Grants == nil {
		WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
		return false
	}

	grants, err := h.Grants.ListActiveForGranteeBucket(r.Context(), principal.UserID, bucket.Name)
	if err != nil {
		h.logError("list grants for bucket relationship", err, bucket.Name, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return false
	}
	if len(grants) == 0 {
		WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
		return false
	}
	return true
}

func (h *ObjectHandlers) evaluator() *authz.Evaluator {
	if h.Authz != nil {
		return h.Authz
	}
	// Fail closed when no evaluator is wired.
	return &authz.Evaluator{}
}

// loadBucket loads bucket metadata without authorization.
func (h *ObjectHandlers) loadBucket(w http.ResponseWriter, r *http.Request, bucketName string) (*metadata.Bucket, bool) {
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
	return bucket, true
}

// loadBucketForAction loads the bucket and authorizes action-specific access.
func (h *ObjectHandlers) loadBucketForAction(w http.ResponseWriter, r *http.Request, bucketName, action, objectKey, listPrefix string) (*metadata.Bucket, bool) {
	bucket, ok := h.loadBucket(w, r, bucketName)
	if !ok {
		return nil, false
	}
	if !h.authorize(w, r, action, bucketName, objectKey, listPrefix, bucket) {
		return nil, false
	}
	return bucket, true
}

// ensureBucketAction authorizes the given action on an existing bucket.
// objectKey and listPrefix are optional depending on the action.
func (h *ObjectHandlers) ensureBucketAction(w http.ResponseWriter, r *http.Request, bucketName, action, objectKey, listPrefix string) bool {
	_, ok := h.loadBucketForAction(w, r, bucketName, action, objectKey, listPrefix)
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
