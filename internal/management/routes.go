package management

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

// Deps holds all repository and auth dependencies for the management API.
type Deps struct {
	Buckets       metadata.BucketRepository
	Objects       metadata.ObjectRepository
	Users         metadata.UserRepository
	Authenticator auth.Authenticator
}

// RegisterRoutes mounts all /api/management/* endpoints onto r.
// All routes require Bearer Token authentication.
func RegisterRoutes(r chi.Router, d Deps) {
	r.Route("/api/management", func(api chi.Router) {
		api.Use(auth.RequireAuthentication(d.Authenticator, mgmtAuthResponder))

		api.Get("/metrics", handleMetrics(d))

		api.Get("/buckets", handleListBuckets(d))
		api.Get("/buckets/{bucket}/objects", handleListObjects(d))
		api.Get("/buckets/{bucket}/objects/*", handleGetObject(d))

		api.Get("/keys", handleListKeys(d))
		api.Post("/keys", handleCreateKey(d))
		api.Delete("/keys/{id}", handleDeleteKey(d))
	})
}

// mgmtAuthResponder writes the correct HTTP status and JSON body for auth
// failures on management endpoints. Mirrors the writeJSONAuthError pattern
// used in cmd/server/main.go for the S3 JSON routes.
func mgmtAuthResponder(w http.ResponseWriter, _ *http.Request, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch {
	case errors.Is(err, auth.ErrMissingAuth):
		w.Header().Set("WWW-Authenticate", `Bearer realm="fbs"`)
		w.WriteHeader(http.StatusUnauthorized)
	case errors.Is(err, auth.ErrUnsupportedScheme):
		w.WriteHeader(http.StatusUnauthorized)
	case errors.Is(err, auth.ErrInactiveUser), errors.Is(err, auth.ErrForbidden):
		w.WriteHeader(http.StatusForbidden)
	case errors.Is(err, auth.ErrInternal):
		w.WriteHeader(http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusUnauthorized)
	}
	json.NewEncoder(w).Encode(map[string]string{"error": "auth failed"})
}
