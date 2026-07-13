package management

import "github.com/go-chi/chi/v5"

// RegisterAdminRoutes registers admin-only management routes.
func RegisterAdminRoutes(r chi.Router, h *Handlers) {
	r.Get("/metrics", h.Metrics)
	r.Get("/config", h.ConfigInfo)
	r.Get("/activity", h.ListActivity)
	r.Get("/buckets", h.ListBuckets)
	r.Get("/buckets/{bucket}", h.GetBucket)
	r.Delete("/buckets/{bucket}", h.DeleteBucket)
	r.Post("/buckets/{bucket}/empty", h.EmptyBucket)
	r.Get("/buckets/{bucket}/objects", h.ListObjects)
	r.Get("/buckets/{bucket}/objects/*", h.GetObject)
	r.Post("/buckets/{bucket}/objects/*", h.CreatePublicObjectURL)
	r.Get("/keys", h.ListKeys)
	r.Post("/keys", h.CreateKey)
	r.Patch("/keys/{id}", h.PatchKey)
	r.Delete("/keys/{id}", h.DeleteKey)
	r.Get("/users/{userID}/grants", h.ListUserGrants)
}

// RegisterGrantRoutes registers grant and ownership routes that allow
// authenticated admins or bucket owners (and self-list for my grants).
// Call after authentication middleware, without requiring admin role.
func RegisterGrantRoutes(r chi.Router, h *Handlers) {
	r.Get("/grants/me", h.ListMyGrants)
	r.Get("/buckets/{bucket}/grants", h.ListBucketGrants)
	r.Post("/buckets/{bucket}/grants", h.CreateBucketGrants)
	r.Patch("/buckets/{bucket}/grants/{grantID}", h.PatchBucketGrant)
	r.Delete("/buckets/{bucket}/grants/{grantID}", h.DeleteBucketGrant)
	r.Post("/buckets/{bucket}/transfer-ownership", h.TransferBucketOwnership)
}

// RegisterRoutes registers all management routes under a single router.
// Prefer RegisterAdminRoutes + RegisterGrantRoutes when role middleware differs.
func RegisterRoutes(r chi.Router, h *Handlers) {
	RegisterGrantRoutes(r, h)
	RegisterAdminRoutes(r, h)
}
