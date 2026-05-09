package management

import "github.com/go-chi/chi/v5"

func RegisterRoutes(r chi.Router, h *Handlers) {
	r.Get("/metrics", h.Metrics)
	r.Get("/config", h.ConfigInfo)
	r.Get("/activity", h.ListActivity)
	r.Get("/buckets", h.ListBuckets)
	r.Get("/buckets/{bucket}", h.GetBucket)
	r.Delete("/buckets/{bucket}", h.DeleteBucket)
	r.Post("/buckets/{bucket}/empty", h.EmptyBucket)
	r.Get("/buckets/{bucket}/objects", h.ListObjects)
	r.Get("/buckets/{bucket}/objects/*", h.GetObject)
	r.Get("/keys", h.ListKeys)
	r.Post("/keys", h.CreateKey)
	r.Patch("/keys/{id}", h.PatchKey)
	r.Delete("/keys/{id}", h.DeleteKey)
}
