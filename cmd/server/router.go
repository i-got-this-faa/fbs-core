package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	httpapi "github.com/i-got-this-faa/fbs/internal/http"
	appmiddleware "github.com/i-got-this-faa/fbs/internal/http/middleware"
	"github.com/i-got-this-faa/fbs/internal/iam"
	"github.com/i-got-this-faa/fbs/internal/management"
	"github.com/i-got-this-faa/fbs/internal/s3"
	"github.com/i-got-this-faa/fbs/internal/setup"
)

func newRouter(cfg config.Config, logger *slog.Logger, app *app) http.Handler {
	return httpapi.NewRouter(cfg, logger, func(r chi.Router) {
		s3.RegisterPublicReadRoutes(r, app.objects)
		setup.RegisterRoutes(r, app.setup)
		r.Route("/api/management", func(managementRoutes chi.Router) {
			managementRoutes.Use(auth.RequireAuthentication(app.authChain, management.WriteAuthError))
			// Grant routes: authenticated admin or bucket owner (enforced in handlers).
			management.RegisterGrantRoutes(managementRoutes, app.management)
			managementRoutes.Group(func(adminRoutes chi.Router) {
				adminRoutes.Use(auth.RequireRole(iam.RoleAdmin, management.WriteAuthError))
				management.RegisterAdminRoutes(adminRoutes, app.management)
			})
		})
		r.Group(func(s3Routes chi.Router) {
			s3Routes.Use(appmiddleware.S3Headers)
			s3Routes.Use(auth.RequireAuthentication(app.authChain, writeS3AuthError))
			s3.RegisterBucketRoutes(s3Routes, app.objects)
			s3.RegisterObjectReadRoutes(s3Routes, app.objects)
		})
		r.Group(func(s3Routes chi.Router) {
			s3Routes.Use(appmiddleware.S3Headers)
			s3Routes.Use(auth.RequireAuthentication(app.authChain, writeS3AuthError))
			s3.RegisterObjectMutationRoutes(s3Routes, app.objects)
		})
		// registerExtraRoutes is a no-op unless built with -tags testendpoints,
		// which compiles in the /_health/auth debug endpoint.
		registerExtraRoutes(r, app.authChain, writeJSONAuthError)
	})
}

func writeS3AuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrMissingAuth):
		w.Header().Set("WWW-Authenticate", `Bearer realm="fbs"`)
		s3.WriteS3Error(w, r, http.StatusUnauthorized, "AccessDenied", "Access denied.")
	case errors.Is(err, auth.ErrInactiveUser), errors.Is(err, auth.ErrForbidden):
		s3.WriteS3Error(w, r, http.StatusForbidden, "AccessDenied", "Access denied.")
	case errors.Is(err, auth.ErrInternal):
		s3.WriteS3Error(w, r, http.StatusInternalServerError, "InternalError", "We encountered an internal error. Please try again.")
	default:
		s3.WriteS3Error(w, r, http.StatusUnauthorized, "AccessDenied", "Access denied.")
	}
}

func writeJSONAuthError(w http.ResponseWriter, _ *http.Request, err error) {
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
