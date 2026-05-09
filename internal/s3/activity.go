package s3

import (
	"net/http"

	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func (h *ObjectHandlers) recordActivity(r *http.Request, action, bucketName, key string, size int64, etag string) {
	if h.Activity == nil {
		return
	}

	actorUserID := ""
	if principal, ok := auth.PrincipalFromContext(r.Context()); ok {
		actorUserID = principal.UserID
	}

	if err := h.Activity.Create(r.Context(), &metadata.ObjectActivity{
		ID:          h.newID(),
		Action:      action,
		BucketName:  bucketName,
		ObjectKey:   key,
		Size:        size,
		ETag:        etag,
		ActorUserID: actorUserID,
		CreatedAt:   h.now(),
	}); err != nil {
		h.logError("record object activity", err, bucketName, key, "")
	}
}
