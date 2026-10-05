package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/i-got-this-faa/fbs/internal/config"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/s3"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

// run starts the server and blocks until it stops. Returning instead of
// exiting lets deferred cleanup, such as closing the database, always run.
func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger.Info("initializing database", "db_path", cfg.DBPath)
	db, err := metadata.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open metadata db: %w", err)
	}
	defer db.Close()

	store, err := storage.New(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("initialize storage engine: %w", err)
	}
	if err := reconcileStorage(context.Background(), store, db); err != nil {
		return err
	}
	if cfg.DevMode {
		logger.Warn("dev mode enabled: authentication is bypassed, do not expose this server remotely")
	}

	app, err := newApp(cfg, db, store, logger)
	if err != nil {
		return err
	}
	if err := logFirstStartSetup(cfg, app.bootstrap, logger); err != nil {
		return err
	}

	cleanupCtx, cancelCleanup := context.WithCancel(context.Background())
	defer cancelCleanup()
	go s3.StaleMultipartCleanup(cleanupCtx, app.multipartUploads, store, cfg.MultipartTTL, cfg.MultipartCleanupInterval, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, cfg, newRouter(cfg, logger, app), logger)
}
