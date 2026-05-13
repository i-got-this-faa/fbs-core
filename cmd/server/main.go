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
	"strings"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	httpapi "github.com/i-got-this-faa/fbs/internal/http"
	"github.com/i-got-this-faa/fbs/internal/management"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/publicread"
	"github.com/i-got-this-faa/fbs/internal/s3"
	"github.com/i-got-this-faa/fbs/internal/server"
	"github.com/i-got-this-faa/fbs/internal/setup"
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

	rawObjectRepo := metadata.NewObjectRepository(db)
	if err := storageEngine.Reconcile(context.Background(), func(bucketName string) ([]string, error) {
		return listKnownStoragePaths(context.Background(), rawObjectRepo, bucketName)
	}); err != nil {
		logger.Error("failed to reconcile storage engine", "error", err)
		os.Exit(1)
	}

	if cfg.DevMode {
		logger.Warn("dev mode enabled: authentication is bypassed, do not expose this server remotely")
	}

	userRepo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	bootstrapRepo := metadata.NewBootstrapRepository(db)
	var authenticators []auth.Authenticator
	if cfg.DevMode {
		authenticators = append(authenticators, &auth.DevAuthenticator{})
	}
	authenticators = append(authenticators, &auth.BearerAuthenticator{Repo: userRepo})
	authenticators = append(authenticators, &auth.SigV4Authenticator{Repo: sigv4Repo})
	authChain := &auth.ChainAuthenticator{Authenticators: authenticators}

	var managementAuthenticators []auth.Authenticator
	if cfg.DevMode {
		managementAuthenticators = append(managementAuthenticators, &auth.DevAuthenticator{})
	}
	managementAuthenticators = append(managementAuthenticators, &auth.BearerAuthenticator{Repo: userRepo})
	managementAuthenticators = append(managementAuthenticators, &auth.SigV4Authenticator{Repo: sigv4Repo})
	managementAuthChain := &auth.ChainAuthenticator{Authenticators: managementAuthenticators}

	rawBucketRepo := metadata.NewBucketRepository(db)
	bucketRepo := rawBucketRepo
	objectRepo := rawObjectRepo
	if cfg.MetadataCacheSizeBytes > 0 {
		cache := metadata.NewMetadataCache(cfg.MetadataCacheSizeBytes)
		bucketRepo = metadata.NewCachedBucketRepository(rawBucketRepo, cache)
		objectRepo = metadata.NewCachedObjectRepository(rawObjectRepo, cache)
	}

	var publicReadSigner *publicread.Signer
	if strings.TrimSpace(cfg.PublicReadSigningSecret) != "" {
		publicReadSigner, err = publicread.NewSigner(cfg.PublicReadSigningSecret, nil)
		if err != nil {
			logger.Error("failed to initialize public read signer", "error", err)
			os.Exit(1)
		}
	}

	managementHandlers := &management.Handlers{
		Management:       metadata.NewManagementRepository(db),
		Buckets:          bucketRepo,
		Objects:          objectRepo,
		Activity:         metadata.NewActivityRepository(db),
		Users:            userRepo,
		Storage:          storageEngine,
		Config:           cfg,
		PublicReadSigner: publicReadSigner,
	}
	objectHandlers := &s3.ObjectHandlers{
		Users:            userRepo,
		Buckets:          bucketRepo,
		Objects:          objectRepo,
		Activity:         metadata.NewActivityRepository(db),
		Storage:          storageEngine,
		Logger:           logger,
		S3CacheControl:   cfg.S3CacheControl,
		PublicReadSigner: publicReadSigner,
	}
	setupHandlers := &setup.Handlers{
		Bootstrap: bootstrapRepo,
		Config:    cfg,
	}

	userCount, err := bootstrapRepo.UserCount(context.Background())
	if err != nil {
		logger.Error("failed to inspect first start setup state", "error", err)
		os.Exit(1)
	}
	if userCount == 0 {
		logger.Info("first start setup required", "setup_url", startupSetupURL(cfg))
	}

	router := httpapi.NewRouter(cfg, logger, func(r chi.Router) {
		s3.RegisterPublicReadRoutes(r, objectHandlers)
		setup.RegisterRoutes(r, setupHandlers)
		r.Route("/api/management", func(managementRoutes chi.Router) {
			managementRoutes.Use(auth.RequireAuthentication(managementAuthChain, management.WriteAuthError))
			managementRoutes.Use(auth.RequireRole("admin", management.WriteAuthError))
			management.RegisterRoutes(managementRoutes, managementHandlers)
		})
		r.Group(func(s3Routes chi.Router) {
			s3Routes.Use(auth.RequireAuthentication(authChain, writeS3AuthError))
			s3.RegisterBucketRoutes(s3Routes, objectHandlers)
			s3.RegisterObjectReadRoutes(s3Routes, objectHandlers)
		})
		r.Group(func(s3Routes chi.Router) {
			s3Routes.Use(auth.RequireAuthentication(authChain, writeS3AuthError))
			s3.RegisterObjectMutationRoutes(s3Routes, objectHandlers)
		})
		// registerExtraRoutes is a no-op unless built with -tags testendpoints,
		// which compiles in the /_health/auth debug endpoint.
		registerExtraRoutes(r, authChain, writeJSONAuthError)
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

func startupSetupURL(cfg config.Config) string {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
	if baseURL == "" {
		addr := strings.TrimSpace(cfg.HTTPAddr)
		if strings.HasPrefix(addr, ":") {
			addr = "127.0.0.1" + addr
		}
		baseURL = "http://" + addr
	}
	return baseURL + "/api/setup/status"
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
