package management

import "github.com/go-chi/chi/v5"

func RegisterRoutes(r chi.Router, h *Handlers) {
	r.Get("/metrics", h.Metrics)
	r.Get("/buckets", h.ListBuckets)
	r.Delete("/buckets/{bucket}", h.DeleteBucket)
	r.Get("/buckets/{bucket}/objects", h.ListObjects)
	r.Get("/buckets/{bucket}/objects/*", h.GetObject)
	r.Get("/keys", h.ListKeys)
	r.Post("/keys", h.CreateKey)
	r.Delete("/keys/{id}", h.DeleteKey)
}
