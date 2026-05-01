//go:build !testendpoints

package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
)

func registerExtraRoutes(_ chi.Router, _ auth.Authenticator, _ func(http.ResponseWriter, *http.Request, error)) {
}
