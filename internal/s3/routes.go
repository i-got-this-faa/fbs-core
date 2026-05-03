package s3

import "github.com/go-chi/chi/v5"

func RegisterBucketRoutes(r chi.Router, h *ObjectHandlers) {
	r.Put("/{bucket}", h.CreateBucket)
	r.Get("/{bucket}", h.ListObjectsV2)
}

func RegisterObjectRoutes(r chi.Router, h *ObjectHandlers) {
	r.Put("/{bucket}/*", h.PutObject)
	r.Get("/{bucket}/*", h.GetObject)
	r.Head("/{bucket}/*", h.HeadObject)
	r.Delete("/{bucket}/*", h.DeleteObject)
}
