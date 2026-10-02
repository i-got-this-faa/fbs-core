package s3

import (
	"net/http"

	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/objectops"
)

// recordActivity audits a completed operation. Activity is not part of the
// operation, so a storage failure is logged and the request still succeeds.
func (h *ObjectHandlers) recordActivity(r *http.Request, action metadata.ActivityAction, bucketName, key string, size int64, etag string) {
	err := objectops.RecordActivity(r.Context(), h.Activity, metadata.ObjectActivity{
		ID:         h.newID(),
		Action:     action,
		BucketName: bucketName,
		ObjectKey:  key,
		Size:       size,
		ETag:       etag,
		CreatedAt:  h.now(),
	})
	if err != nil {
		h.logError("record object activity", err, bucketName, key, "")
	}
}
