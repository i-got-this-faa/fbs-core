package main

import (
	"context"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"syscall"

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

	router := httpapi.NewRouter(cfg, logger, nil)
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
