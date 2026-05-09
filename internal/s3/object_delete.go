package s3

import (
	"net/http"

	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/objectops"
)

func (h *ObjectHandlers) deleteObject(r *http.Request, bucketName, key string) (*metadata.Object, bool, error) {
	return objectops.DeleteObject(r.Context(), h.Objects, h.Storage, bucketName, key)
}
