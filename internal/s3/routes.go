package s3

import "github.com/go-chi/chi/v5"

func RegisterObjectRoutes(r chi.Router, h *ObjectHandlers) {
	r.Put("/{bucket}/*", h.PutObject)
	r.Get("/{bucket}/*", h.GetObject)
	r.Head("/{bucket}/*", h.HeadObject)
	r.Delete("/{bucket}/*", h.DeleteObject)
}
