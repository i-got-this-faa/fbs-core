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
<<<<<<< Updated upstream
	RegisterObjectReadRoutes(r, h)
	RegisterObjectMutationRoutes(r, h)
}

func RegisterObjectReadRoutes(r chi.Router, h *ObjectHandlers) {
	r.Get("/{bucket}/*", h.DispatchObjectGet)
	r.Head("/{bucket}/*", h.HeadObject)
}

func RegisterObjectMutationRoutes(r chi.Router, h *ObjectHandlers) {
	r.Put("/{bucket}/*", h.DispatchObjectPut)
	r.Delete("/{bucket}/*", h.DispatchObjectDelete)
}

func RegisterPublicReadRoutes(r chi.Router, h *ObjectHandlers) {
	r.Get("/public/{bucket}/*", h.PublicReadObject)
	r.Head("/public/{bucket}/*", h.PublicReadObject)
=======
	r.Put("/{bucket}/*", h.DispatchPut)
	r.Get("/{bucket}/*", h.GetObject)
	r.Head("/{bucket}/*", h.HeadObject)
	r.Delete("/{bucket}/*", h.DispatchDelete)
	r.Post("/{bucket}/*", h.DispatchPost)
>>>>>>> Stashed changes
}
