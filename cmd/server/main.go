package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	httpapi "github.com/i-got-this-faa/fbs/internal/http"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/server"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger.Info("initializing database", "db_path", cfg.DBPath)
	db, err := metadata.Open(cfg.DBPath)
	if err != nil {
		logger.Error("failed to open metadata db", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	storageEngine, err := storage.New(cfg.DataDir)
	if err != nil {
		logger.Error("failed to initialize storage engine", "error", err)
		os.Exit(1)
	}

	objectRepo := metadata.NewObjectRepository(db)
	if err := storageEngine.Reconcile(context.Background(), func(bucketName string) ([]string, error) {
		return listKnownStoragePaths(context.Background(), objectRepo, bucketName)
	}); err != nil {
		logger.Error("failed to reconcile storage engine", "error", err)
		os.Exit(1)
	}

	if cfg.DevMode {
		logger.Warn("dev mode enabled: authentication is bypassed, do not expose this server remotely")
	}

	userRepo := metadata.NewUserRepository(db)
	var authenticators []auth.Authenticator
	if cfg.DevMode {
		authenticators = append(authenticators, &auth.DevAuthenticator{})
	}
	authenticators = append(authenticators, &auth.BearerAuthenticator{Repo: userRepo})
	authChain := &auth.ChainAuthenticator{Authenticators: authenticators}

	writeJSONAuthError := func(w http.ResponseWriter, _ *http.Request, err error) {
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

	router := httpapi.NewRouter(cfg, logger, func(r chi.Router) {
		r.Group(func(authGroup chi.Router) {
			authGroup.Use(auth.RequireAuthentication(authChain, writeJSONAuthError))
			authGroup.Get("/_health/auth", func(w http.ResponseWriter, r *http.Request) {
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
	})
	srv := server.New(cfg, router)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	logger.Info(
		"starting server",
		"http_addr", cfg.HTTPAddr,
		"db_path", cfg.DBPath,
		"data_dir", cfg.DataDir,
		"public_base_url", cfg.PublicBaseURL,
		"cors_allowed_origins", cfg.CORSAllowedOrigins,
	)

	select {
	case err := <-errCh:
		if err != nil {
			logger.Error("server exited with error", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		logger.Info("shutting down server")
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown failed", "error", err)
			os.Exit(1)
		}

		if err := <-errCh; err != nil {
			logger.Error("server exited with error", "error", err)
			os.Exit(1)
		}
	}
}

func listKnownStoragePaths(ctx context.Context, repo metadata.ObjectRepository, bucketName string) ([]string, error) {
	startAfter := ""
	var storagePaths []string

	for {
		objects, isTruncated, err := repo.List(ctx, bucketName, "", startAfter, math.MaxInt32-1)
		if err != nil {
			return nil, err
		}
		if len(objects) == 0 {
			return storagePaths, nil
		}

		for _, object := range objects {
			storagePaths = append(storagePaths, object.StoragePath)
		}

		if !isTruncated {
			return storagePaths, nil
		}

		startAfter = objects[len(objects)-1].Key
	}
}
