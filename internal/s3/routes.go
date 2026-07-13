package s3

import "github.com/go-chi/chi/v5"

func RegisterBucketRoutes(r chi.Router, h *ObjectHandlers) {
	r.Get("/", h.ListBuckets)
	r.Post("/{bucket}", h.DispatchBucketPost)
	r.Put("/{bucket}", h.DispatchBucketPut)
	r.Get("/{bucket}", h.DispatchBucketGet)
	r.Head("/{bucket}", h.HeadBucket)
	// Multi-object delete is POST /{bucket}?delete per the S3 API (boto3/aws-cli).
	// DELETE /{bucket}?delete is also accepted for older clients and in-repo tests.
	r.Post("/{bucket}", h.DispatchBucketPost)
	r.Put("/{bucket}", h.DispatchBucketPut)
	r.Get("/{bucket}", h.DispatchBucketGet)
	r.Head("/{bucket}", h.HeadBucket)
	// Multi-object delete is POST /{bucket}?delete per the S3 API (boto3/aws-cli).
	// DELETE /{bucket}?delete is also accepted for older clients and in-repo tests.
	r.Delete("/{bucket}", h.DispatchBucketDelete)
	r.Delete("/{bucket}", h.DispatchBucketDelete)
}

func RegisterObjectRoutes(r chi.Router, h *ObjectHandlers) {
	RegisterObjectReadRoutes(r, h)
	RegisterObjectMutationRoutes(r, h)
}

func RegisterObjectReadRoutes(r chi.Router, h *ObjectHandlers) {
	r.Get("/{bucket}/*", h.DispatchObjectGet)
	r.Head("/{bucket}/*", h.HeadObject)
}

func RegisterObjectMutationRoutes(r chi.Router, h *ObjectHandlers) {
	r.Put("/{bucket}/*", h.DispatchObjectPut)
	r.Post("/{bucket}/*", h.DispatchPost)
	r.Delete("/{bucket}/*", h.DispatchObjectDelete)
}

func RegisterPublicReadRoutes(r chi.Router, h *ObjectHandlers) {
	r.Get("/public/{bucket}/*", h.PublicReadObject)
	r.Head("/public/{bucket}/*", h.PublicReadObject)
}
