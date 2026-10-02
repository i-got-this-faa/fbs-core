package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	"github.com/i-got-this-faa/fbs/internal/management"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/objectops"
	"github.com/i-got-this-faa/fbs/internal/publicread"
	"github.com/i-got-this-faa/fbs/internal/s3"
	"github.com/i-got-this-faa/fbs/internal/setup"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

// app holds the wired handlers and the dependencies that run needs directly.
type app struct {
	objects          *s3.ObjectHandlers
	management       *management.Handlers
	setup            *setup.Handlers
	authChain        *auth.ChainAuthenticator
	bootstrap        metadata.BootstrapRepository
	multipartUploads metadata.MultipartUploadRepository
}

func newApp(cfg config.Config, db *sql.DB, store storage.DiskEngine, logger *slog.Logger) (*app, error) {
	var publicReadSigner *publicread.Signer
	if strings.TrimSpace(cfg.PublicReadSigningSecret) != "" {
		signer, err := publicread.NewSigner(cfg.PublicReadSigningSecret, nil)
		if err != nil {
			return nil, fmt.Errorf("initialize public read signer: %w", err)
		}
		publicReadSigner = signer
	}

	bucketRepo := metadata.NewBucketRepository(db)
	objectRepo := metadata.NewObjectRepository(db)
	if cfg.MetadataCacheSizeBytes > 0 {
		cache := metadata.NewMetadataCache(cfg.MetadataCacheSizeBytes)
		bucketRepo = metadata.NewCachedBucketRepository(bucketRepo, cache)
		objectRepo = metadata.NewCachedObjectRepository(objectRepo, cache)
	}
	userRepo := metadata.NewUserRepository(db)
	grantRepo := metadata.NewGrantRepository(db)
	activityRepo := metadata.NewActivityRepository(db)
	multipartRepo := metadata.NewMultipartUploadRepository(db)

	return &app{
		objects: &s3.ObjectHandlers{
			Users:            userRepo,
			Buckets:          bucketRepo,
			Objects:          objectRepo,
			Activity:         activityRepo,
			Grants:           grantRepo,
			Authz:            s3.NewAuthzEvaluator(grantRepo),
			Storage:          store,
			Logger:           logger,
			S3CacheControl:   cfg.S3CacheControl,
			PublicReadSigner: publicReadSigner,
			MultipartUploads: multipartRepo,
		},
		management: &management.Handlers{
			Management:       metadata.NewManagementRepository(db),
			Buckets:          bucketRepo,
			Objects:          objectRepo,
			Activity:         activityRepo,
			Users:            userRepo,
			Grants:           grantRepo,
			Storage:          store,
			Config:           cfg,
			PublicReadSigner: publicReadSigner,
			Logger:           logger,
		},
		setup: &setup.Handlers{
			Bootstrap: metadata.NewBootstrapRepository(db),
			Config:    cfg,
		},
		authChain:        newAuthChain(cfg.DevMode, userRepo, metadata.NewSigV4UserRepository(db)),
		bootstrap:        metadata.NewBootstrapRepository(db),
		multipartUploads: multipartRepo,
	}, nil
}

// newAuthChain builds the authenticator chain shared by the S3 and Management
// APIs. The dev authenticator, when enabled, runs first so it short-circuits.
func newAuthChain(devMode bool, users metadata.UserRepository, sigv4Users metadata.SigV4UserRepository) *auth.ChainAuthenticator {
	var authenticators []auth.Authenticator
	if devMode {
		authenticators = append(authenticators, &auth.DevAuthenticator{})
	}
	authenticators = append(authenticators,
		&auth.BearerAuthenticator{Repo: users},
		&auth.SigV4Authenticator{Repo: sigv4Users},
	)
	return &auth.ChainAuthenticator{Authenticators: authenticators}
}

// reconcileStorage removes backing files and multipart part directories that
// no metadata row references, which a crash between write and commit can leave.
func reconcileStorage(ctx context.Context, store storage.DiskEngine, db *sql.DB) error {
	objects := metadata.NewObjectRepository(db)
	err := store.Reconcile(ctx, func(bucketName string) ([]string, error) {
		bucketObjects, err := objectops.ListAllObjects(ctx, objects, bucketName)
		if err != nil {
			return nil, err
		}
		paths := make([]string, 0, len(bucketObjects))
		for _, object := range bucketObjects {
			paths = append(paths, object.StoragePath)
		}
		return paths, nil
	})
	if err != nil {
		return fmt.Errorf("reconcile storage engine: %w", err)
	}

	uploads := metadata.NewMultipartUploadRepository(db)
	err = store.ReconcileMultipartTmp(ctx, func() (map[string]struct{}, error) {
		ids, err := uploads.ListAllUploadIDs(ctx)
		if err != nil {
			return nil, err
		}
		known := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			known[id] = struct{}{}
		}
		return known, nil
	})
	if err != nil {
		return fmt.Errorf("reconcile multipart tmp: %w", err)
	}
	return nil
}

func logFirstStartSetup(cfg config.Config, bootstrap metadata.BootstrapRepository, logger *slog.Logger) error {
	userCount, err := bootstrap.UserCount(context.Background())
	if err != nil {
		return fmt.Errorf("inspect first start setup state: %w", err)
	}
	if userCount == 0 {
		logger.Info("first start setup required", "setup_url", startupSetupURL(cfg))
	}
	return nil
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
