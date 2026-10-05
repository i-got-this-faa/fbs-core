package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/i-got-this-faa/fbs/internal/config"
)

// serve runs the HTTP server until it fails or ctx is done, then shuts it
// down gracefully within cfg.ShutdownTimeout.
func serve(ctx context.Context, cfg config.Config, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

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
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}
