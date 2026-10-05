package s3

import "github.com/go-chi/chi/v5"

func RegisterBucketRoutes(r chi.Router, h *ObjectHandlers) {
	r.Get("/", h.ListBuckets)
	// Multi-object delete is POST /{bucket}?delete per the S3 API (boto3/aws-cli).
	// DELETE /{bucket}?delete is also accepted because fbs-web sends DeleteObjects that way.
	r.Post("/{bucket}", h.DispatchBucketPost)
	r.Put("/{bucket}", h.DispatchBucketPut)
	r.Get("/{bucket}", h.DispatchBucketGet)
	r.Head("/{bucket}", h.HeadBucket)
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

// RegisterShareLinkRoutes serves share links. The optional trailing segment
// lets links carry a file name (for example /s/abc123/clip.mp4) that some
// chat clients use when deciding how to embed media; it is ignored.
func RegisterShareLinkRoutes(r chi.Router, h *ObjectHandlers) {
	for _, pattern := range []string{"/s/{code}", "/s/{code}/*"} {
		r.Get(pattern, h.ShareLinkReadObject)
		r.Head(pattern, h.ShareLinkReadObject)
	}
}
