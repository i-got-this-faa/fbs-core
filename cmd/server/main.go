package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/i-got-this-faa/fbs/internal/config"
	httpapi "github.com/i-got-this-faa/fbs/internal/http"
	"github.com/i-got-this-faa/fbs/internal/server"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
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
