package s3

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/iam"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

// shareLinkCacheControl lets shared caches keep the bytes but forces them to
// revalidate, so a revoked or expired link stops serving promptly.
const shareLinkCacheControl = "public, max-age=0, must-revalidate"

// ShareLinkReadObject serves the object behind a share link code. Unknown,
// expired, and unauthorized links all return 404 so codes cannot be probed.
func (h *ObjectHandlers) ShareLinkReadObject(w http.ResponseWriter, r *http.Request) {
	link, ok := h.loadActiveShareLink(w, r, chi.URLParam(r, "code"))
	if !ok {
		return
	}

	obj, err := h.Objects.GetByKey(r.Context(), link.BucketName, link.ObjectKey)
	if errors.Is(err, metadata.ErrObjectNotFound) {
		writePublicReadError(w, http.StatusNotFound)
		return
	}
	if err != nil {
		h.logError("load share link object metadata", err, link.BucketName, link.ObjectKey, "")
		writePublicReadError(w, http.StatusInternalServerError)
		return
	}

	h.serveObject(w, r, obj, shareLinkCacheControl, link.ResponseContentDisposition, mapPublicStorageReadError)
}

func (h *ObjectHandlers) loadActiveShareLink(w http.ResponseWriter, r *http.Request, code string) (*metadata.ShareLink, bool) {
	if h.ShareLinks == nil || code == "" {
		writePublicReadError(w, http.StatusNotFound)
		return nil, false
	}

	link, err := h.ShareLinks.GetByCode(r.Context(), code)
	if errors.Is(err, metadata.ErrShareLinkNotFound) {
		writePublicReadError(w, http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		h.logError("load share link", err, "", "", "")
		writePublicReadError(w, http.StatusInternalServerError)
		return nil, false
	}
	if link.IsExpired(h.now()) {
		writePublicReadError(w, http.StatusNotFound)
		return nil, false
	}

	allowed, err := h.creatorCanRead(r, link)
	if err != nil {
		h.logError("authorize share link creator", err, link.BucketName, link.ObjectKey, "")
		writePublicReadError(w, http.StatusInternalServerError)
		return nil, false
	}
	if !allowed {
		writePublicReadError(w, http.StatusNotFound)
		return nil, false
	}

	return link, true
}

// creatorCanRead re-checks at serve time that the link creator is still an
// active user allowed to read the object, so revoking a user's access also
// disables every link they shared.
func (h *ObjectHandlers) creatorCanRead(r *http.Request, link *metadata.ShareLink) (bool, error) {
	creator, err := h.Users.GetByID(r.Context(), link.CreatedBy)
	if errors.Is(err, metadata.ErrUserNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !creator.IsActive {
		return false, nil
	}

	bucket, err := h.Buckets.GetByName(r.Context(), link.BucketName)
	if errors.Is(err, metadata.ErrBucketNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return h.evaluator().Allow(r.Context(), authz.DecisionRequest{
		Principal:     auth.Principal{UserID: creator.ID, Role: creator.Role},
		Action:        iam.ActionGetObject,
		Bucket:        link.BucketName,
		ObjectKey:     link.ObjectKey,
		BucketOwnerID: bucket.OwnerID,
	})
}
