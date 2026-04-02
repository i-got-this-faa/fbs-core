package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	stdhttp "net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/i-got-this-faa/fbs/internal/config"
	appmiddleware "github.com/i-got-this-faa/fbs/internal/http/middleware"
)

type statusResponse struct {
	Status string `json:"status"`
}

func NewRouter(cfg config.Config, logger *slog.Logger, registerExtras func(chi.Router)) stdhttp.Handler {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	router := chi.NewRouter()
	router.Use(appmiddleware.Logging(logger))
	router.Use(appmiddleware.Recovery(logger))
	router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSAllowedOrigins,
		AllowedMethods:   []string{stdhttp.MethodGet, stdhttp.MethodHead, stdhttp.MethodPost, stdhttp.MethodPut, stdhttp.MethodDelete, stdhttp.MethodOptions},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Content-Length", "Origin", "X-Amz-Content-Sha256", "X-Amz-Date", "X-Amz-Security-Token", "X-Requested-With"},
		ExposedHeaders:   []string{"ETag"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	router.Get("/healthz", serveStatus("ok"))
	router.Get("/readyz", serveStatus("ready"))

	if registerExtras != nil {
		registerExtras(router)
	}

	return router
}

func serveStatus(status string) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		writeJSON(w, stdhttp.StatusOK, statusResponse{Status: status})
	}
}

func writeJSON(w stdhttp.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		stdhttp.Error(w, stdhttp.StatusText(stdhttp.StatusInternalServerError), stdhttp.StatusInternalServerError)
	}
}
