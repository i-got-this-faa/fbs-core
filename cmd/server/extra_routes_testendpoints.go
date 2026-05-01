//go:build testendpoints

package main

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
)

func registerExtraRoutes(r chi.Router, chain auth.Authenticator, errHandler func(http.ResponseWriter, *http.Request, error)) {
	r.Group(func(testRoutes chi.Router) {
		testRoutes.Use(auth.RequireAuthentication(chain, errHandler))
		testRoutes.Get("/_health/auth", func(w http.ResponseWriter, r *http.Request) {
			p, _ := auth.PrincipalFromContext(r.Context())
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			json.NewEncoder(w).Encode(map[string]any{
				"status":   "authenticated",
				"user_id":  p.UserID,
				"role":     p.Role,
				"dev_mode": p.DevMode,
			})
		})
	})
}
