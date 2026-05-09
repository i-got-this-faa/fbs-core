package s3

import "github.com/go-chi/chi/v5"

func RegisterBucketRoutes(r chi.Router, h *ObjectHandlers) {
	r.Get("/", h.ListBuckets)
	r.Put("/{bucket}", h.DispatchBucketPut)
	r.Get("/{bucket}", h.DispatchBucketGet)
	r.Head("/{bucket}", h.HeadBucket)
	r.Delete("/{bucket}", h.DispatchBucketDelete)
}

func RegisterObjectRoutes(r chi.Router, h *ObjectHandlers) {
	r.Put("/{bucket}/*", h.DispatchObjectPut)
	r.Get("/{bucket}/*", h.DispatchObjectGet)
	r.Head("/{bucket}/*", h.HeadObject)
	r.Delete("/{bucket}/*", h.DispatchObjectDelete)
}
