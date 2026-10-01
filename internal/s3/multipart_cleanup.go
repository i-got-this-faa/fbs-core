package s3

import (
	"context"
	"log/slog"
	"time"

	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

// StaleMultipartCleanup removes multipart uploads older than ttl every interval
// until ctx is done.
func StaleMultipartCleanup(ctx context.Context, uploads metadata.MultipartUploadRepository, store storage.DiskEngine, ttl time.Duration, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removeStaleUploads(ctx, uploads, store, ttl, logger)
		}
	}
}

func removeStaleUploads(ctx context.Context, uploads metadata.MultipartUploadRepository, store storage.DiskEngine, ttl time.Duration, logger *slog.Logger) {
	stale, err := uploads.ListStale(ctx, time.Now().UTC().Add(-ttl))
	if err != nil {
		logger.Error("list stale multipart uploads", "error", err)
		return
	}
	for _, upload := range stale {
		// A claimed upload gets a longer grace period, in case a long-running
		// completion is still assembling it.
		if upload.Status != metadata.MultipartUploadStatusActive && time.Since(upload.StatusUpdatedAt) < ttl*2 {
			continue
		}
		removeStaleUpload(ctx, uploads, store, upload, logger)
	}
}

func removeStaleUpload(ctx context.Context, uploads metadata.MultipartUploadRepository, store storage.DiskEngine, upload metadata.MultipartUpload, logger *slog.Logger) {
	claimed := upload.Status == metadata.MultipartUploadStatusActive
	if claimed {
		// Claim an active upload first so a concurrent part upload cannot race the delete.
		if err := uploads.ClaimUpload(ctx, upload.ID, metadata.MultipartUploadStatusAborted); err != nil {
			logger.Error("claim stale multipart upload", "upload_id", upload.ID, "error", err)
			return
		}
	}

	// Delete metadata first so rows never point to missing files.
	if err := uploads.Delete(ctx, upload.ID); err != nil {
		logger.Error("delete stale multipart upload", "upload_id", upload.ID, "error", err)
		if claimed {
			resetCtx, cancel := withCleanupTimeout()
			defer cancel()
			if err := uploads.SetUploadStatus(resetCtx, upload.ID, metadata.MultipartUploadStatusActive); err != nil {
				logger.Error("reset upload status after failed stale delete", "upload_id", upload.ID, "error", err)
			}
		}
		return
	}

	cleanupCtx, cancel := withCleanupTimeout()
	defer cancel()
	if err := store.DeleteUploadParts(cleanupCtx, upload.ID); err != nil {
		logger.Error("delete stale upload parts", "upload_id", upload.ID, "error", err)
	}
}
