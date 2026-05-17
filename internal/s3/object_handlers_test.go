package s3

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	httpapi "github.com/i-got-this-faa/fbs/internal/http"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/publicread"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

type objectTestEnv struct {
	router           http.Handler
	users            metadata.UserRepository
	buckets          metadata.BucketRepository
	objects          metadata.ObjectRepository
	multipartUploads metadata.MultipartUploadRepository
	storage          storage.DiskEngine
	sigv4            auth.SigV4Credentials
	signer           *publicread.Signer
	bucket           string
	dataDir          string
	userID           string
	now              time.Time
}

func newObjectTestEnv(t *testing.T) objectTestEnv {
	t.Helper()

	db, err := metadata.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	userRepo := metadata.NewUserRepository(db)
	_, sigv4, user, err := auth.CreateBearerToken(context.Background(), userRepo, "Test User", "admin")
	if err != nil {
		t.Fatalf("create bearer token: %v", err)
	}

	bucketRepo := metadata.NewBucketRepository(db)
	bucketName := "test-bucket"
	if err := bucketRepo.Create(context.Background(), &metadata.Bucket{
		Name:      bucketName,
		OwnerID:   user.ID,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	dataDir := t.TempDir()
	disk, err := storage.New(dataDir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	objectRepo := metadata.NewCachedObjectRepository(metadata.NewObjectRepository(db), metadata.NewMetadataCache(1024*1024))
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	signer, err := publicread.NewSigner("12345678901234567890123456789012", func() time.Time { return now })
	if err != nil {
		t.Fatalf("new public read signer: %v", err)
	}
	multipartRepo := metadata.NewMultipartUploadRepository(db)
	handlers := &ObjectHandlers{
		Users:            userRepo,
		Buckets:          bucketRepo,
		Objects:          objectRepo,
		MultipartUploads: multipartRepo,
		Storage:          disk,
		Now:              func() time.Time { return now },
		NewID:            newSequentialID(),
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		S3CacheControl:   config.Default().S3CacheControl,
		PublicReadSigner: signer,
		MinPartSize:      1, // small value for testability
	}

	cfg := config.Default()
	router := httpapi.NewRouter(cfg, nil, func(r chi.Router) {
		RegisterPublicReadRoutes(r, handlers)
		r.Group(func(protected chi.Router) {
			protected.Use(auth.RequireAuthentication(&auth.DevAuthenticator{}, func(w http.ResponseWriter, r *http.Request, err error) {
				WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
			}))
			RegisterBucketRoutes(protected, handlers)
			RegisterObjectRoutes(protected, handlers)
		})
	})

	return objectTestEnv{
		router:           router,
		users:            userRepo,
		buckets:          bucketRepo,
		objects:          objectRepo,
		multipartUploads: multipartRepo,
		storage:          disk,
		sigv4:            sigv4,
		signer:           signer,
		bucket:           bucketName,
		dataDir:          dataDir,
		userID:           user.ID,
		now:              now,
	}
}

func newSequentialID() func() string {
	next := 0
	return func() string {
		next++
		return "object-id-" + strconv.Itoa(next)
	}
}

func TestPutObject(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	body := "hello object"

	resp := env.do(t, http.MethodPut, "/"+env.bucket+"/hello.txt", body, map[string]string{
		"Content-Type": "text/plain",
	})

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	wantETag := quotedMD5(body)
	if got := resp.Header().Get("ETag"); got != wantETag {
		t.Fatalf("ETag = %q, want %q", got, wantETag)
	}

	obj, err := env.objects.GetByKey(context.Background(), env.bucket, "hello.txt")
	if err != nil {
		t.Fatalf("get object metadata: %v", err)
	}
	if obj.Size != int64(len(body)) {
		t.Errorf("Size = %d, want %d", obj.Size, len(body))
	}
	if obj.ContentType != "text/plain" {
		t.Errorf("ContentType = %q, want text/plain", obj.ContentType)
	}
}

func TestPutObjectDefaultContentType(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPut, "/"+env.bucket+"/default.bin", "data", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	obj, err := env.objects.GetByKey(context.Background(), env.bucket, "default.bin")
	if err != nil {
		t.Fatalf("get object metadata: %v", err)
	}
	if obj.ContentType != "application/octet-stream" {
		t.Fatalf("ContentType = %q, want application/octet-stream", obj.ContentType)
	}
}

func TestPutObjectContentMD5(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	body := "checksummed"
	resp := env.do(t, http.MethodPut, "/"+env.bucket+"/md5.txt", body, map[string]string{
		"Content-MD5": base64MD5(body),
	})

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
}

func TestPutObjectSHA256Checksum(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	body := "sha256-checksummed"
	resp := env.do(t, http.MethodPut, "/"+env.bucket+"/sha256.txt", body, map[string]string{
		"x-amz-checksum-sha256": base64SHA256(body),
	})

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
}

func TestPutObjectSignedPayloadHashMismatch(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPut, "/"+env.bucket+"/signed-payload.txt", "actual payload", map[string]string{
		"X-Amz-Content-SHA256": hexSHA256("different payload"),
	})

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeBadDigest)

	if _, err := env.objects.GetByKey(context.Background(), env.bucket, "signed-payload.txt"); !errors.Is(err, metadata.ErrObjectNotFound) {
		t.Fatalf("expected no committed metadata, got %v", err)
	}
}

func TestPutObjectMalformedChecksum(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPut, "/"+env.bucket+"/malformed.txt", "payload", map[string]string{
		"x-amz-checksum-sha256": "not-base64",
	})

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeInvalidRequest)
}

func TestPutObjectBadDigest(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPut, "/"+env.bucket+"/bad.txt", "payload", map[string]string{
		"Content-MD5": base64MD5("different payload"),
	})

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeBadDigest)

	if _, err := env.objects.GetByKey(context.Background(), env.bucket, "bad.txt"); !errors.Is(err, metadata.ErrObjectNotFound) {
		t.Fatalf("expected no committed metadata, got %v", err)
	}
	assertNoDataFiles(t, env.dataDir)
}

func TestPutObjectOverwrite(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	first := env.do(t, http.MethodPut, "/"+env.bucket+"/same.txt", "old", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.Code)
	}

	second := env.do(t, http.MethodPut, "/"+env.bucket+"/same.txt", "new payload", nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200", second.Code)
	}

	resp := env.do(t, http.MethodGet, "/"+env.bucket+"/same.txt", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if resp.Body.String() != "new payload" {
		t.Fatalf("body = %q, want new payload", resp.Body.String())
	}
}

func TestPutObjectNestedKey(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	key := "nested/path/file.txt"
	resp := env.do(t, http.MethodPut, "/"+env.bucket+"/"+key, "nested", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("put status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	resp = env.do(t, http.MethodGet, "/"+env.bucket+"/"+key, "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if resp.Body.String() != "nested" {
		t.Fatalf("body = %q, want nested", resp.Body.String())
	}
}

func TestPutObjectNoSuchBucket(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPut, "/missing/file.txt", "payload", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeNoSuchBucket)
}

func TestGetObject(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	body := "download me"
	env.mustPut(t, "download.txt", body)

	resp := env.do(t, http.MethodGet, "/"+env.bucket+"/download.txt", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if resp.Body.String() != body {
		t.Fatalf("body = %q, want %q", resp.Body.String(), body)
	}
	if got := resp.Header().Get("ETag"); got != quotedMD5(body) {
		t.Fatalf("ETag = %q, want %q", got, quotedMD5(body))
	}
	if got := resp.Header().Get("Content-Length"); got != "11" {
		t.Fatalf("Content-Length = %q, want 11", got)
	}
	if got := resp.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", got)
	}
	if got := resp.Header().Get("Last-Modified"); got == "" {
		t.Fatal("expected Last-Modified header")
	}
	if got := resp.Header().Get("Cache-Control"); got != config.Default().S3CacheControl {
		t.Fatalf("Cache-Control = %q, want %q", got, config.Default().S3CacheControl)
	}
}

func TestGetObjectRange(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "range.txt", "range body")

	resp := env.do(t, http.MethodGet, "/"+env.bucket+"/range.txt", "", map[string]string{
		"Range": "bytes=0-3",
	})
	if resp.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206; body=%s", resp.Code, resp.Body.String())
	}
	if resp.Body.String() != "rang" {
		t.Fatalf("body = %q, want rang", resp.Body.String())
	}
	if got := resp.Header().Get("Content-Range"); got != "bytes 0-3/10" {
		t.Fatalf("Content-Range = %q, want bytes 0-3/10", got)
	}
}

func TestGetObjectIfNoneMatch(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	body := "conditional"
	env.mustPut(t, "conditional.txt", body)

	resp := env.do(t, http.MethodGet, "/"+env.bucket+"/conditional.txt", "", map[string]string{
		"If-None-Match": quotedMD5(body),
	})
	if resp.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304; body=%s", resp.Code, resp.Body.String())
	}
}

func TestGetObjectIfModifiedSince(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "modified.txt", "body")

	resp := env.do(t, http.MethodGet, "/"+env.bucket+"/modified.txt", "", map[string]string{
		"If-Modified-Since": env.now.Format(http.TimeFormat),
	})
	if resp.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304; body=%s", resp.Code, resp.Body.String())
	}
}

func TestGetObjectNoSuchKey(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodGet, "/"+env.bucket+"/missing.txt", "", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeNoSuchKey)
}

func TestHeadObject(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "head.txt", "head body")

	resp := env.do(t, http.MethodHead, "/"+env.bucket+"/head.txt", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if resp.Body.Len() != 0 {
		t.Fatalf("HEAD body length = %d, want 0", resp.Body.Len())
	}
	if got := resp.Header().Get("ETag"); got == "" {
		t.Fatal("expected ETag header")
	}
	if got := resp.Header().Get("Cache-Control"); got != config.Default().S3CacheControl {
		t.Fatalf("Cache-Control = %q, want %q", got, config.Default().S3CacheControl)
	}
}

func TestHeadObjectMatchesGetHeaders(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "head-headers.txt", "head body")

	getResp := env.do(t, http.MethodGet, "/"+env.bucket+"/head-headers.txt", "", nil)
	headResp := env.do(t, http.MethodHead, "/"+env.bucket+"/head-headers.txt", "", nil)
	for _, header := range []string{"ETag", "Content-Type", "Last-Modified", "Cache-Control"} {
		if got, want := headResp.Header().Get(header), getResp.Header().Get(header); got != want {
			t.Fatalf("HEAD %s = %q, want GET value %q", header, got, want)
		}
	}
	if headResp.Body.Len() != 0 {
		t.Fatalf("HEAD body length = %d, want 0", headResp.Body.Len())
	}
}

func TestGetObjectMissingBackingFile(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	now := time.Now().UTC()
	if err := env.objects.Create(context.Background(), &metadata.Object{
		ID:          "missing-file",
		BucketName:  env.bucket,
		Key:         "missing-backing.txt",
		Size:        12,
		ETag:        strings.Trim(quotedMD5("missing file"), `"`),
		ContentType: "text/plain",
		StoragePath: env.bucket + "/missing-backing.txt",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("create metadata: %v", err)
	}

	resp := env.do(t, http.MethodGet, "/"+env.bucket+"/missing-backing.txt", "", nil)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeInternalError)
}

func TestPublicReadMissingSignature(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "public.txt", "body")

	resp := env.do(t, http.MethodGet, "/public/"+env.bucket+"/public.txt", "", nil)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
	if got := resp.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestPublicReadExpiredSignature(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "public.txt", "body")

	resp := env.do(t, http.MethodGet, env.publicReadURL("public.txt", -time.Second), "", nil)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
}

func TestPublicReadBadSignature(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "public.txt", "body")

	path := publicread.ObjectPath(env.bucket, "public.txt")
	resp := env.do(t, http.MethodGet, path+"?expires="+strconv.FormatInt(env.now.Add(time.Hour).Unix(), 10)+"&signature="+strings.Repeat("0", 64), "", nil)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
}

func TestPublicReadGet(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "public.txt", "public body")

	resp := env.do(t, http.MethodGet, env.publicReadURL("public.txt", time.Hour), "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if resp.Body.String() != "public body" {
		t.Fatalf("body = %q, want public body", resp.Body.String())
	}
	if got := resp.Header().Get("Cache-Control"); got != "public, max-age=3600, must-revalidate" {
		t.Fatalf("Cache-Control = %q, want public max-age", got)
	}
}

func TestPublicReadHead(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "public-head.txt", "public body")

	resp := env.do(t, http.MethodHead, env.publicReadURL("public-head.txt", time.Hour), "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if resp.Body.Len() != 0 {
		t.Fatalf("HEAD body length = %d, want 0", resp.Body.Len())
	}
	if got := resp.Header().Get("Cache-Control"); got != "public, max-age=3600, must-revalidate" {
		t.Fatalf("Cache-Control = %q, want public max-age", got)
	}
}

func TestPublicReadMissingObject(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodGet, env.publicReadURL("missing.txt", time.Hour), "", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.Code)
	}
	if got := resp.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestPublicReadCacheControlUsesRemainingSignatureLifetime(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "short.txt", "body")

	resp := env.do(t, http.MethodGet, env.publicReadURL("short.txt", 10*time.Second), "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Cache-Control"); got != "public, max-age=10, must-revalidate" {
		t.Fatalf("Cache-Control = %q, want max-age=10", got)
	}
}

func TestPublicReadCacheControlFloorsToActualRemainingSeconds(t *testing.T) {
	t.Parallel()

	handlers := &ObjectHandlers{
		Now: func() time.Time {
			return time.Unix(100, 900_000_000).UTC()
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/public/test-bucket/short.txt?expires=110&signature=sig", nil)

	got := handlers.publicCacheControl(req)
	if got != "public, max-age=9, must-revalidate" {
		t.Fatalf("Cache-Control = %q, want max-age=9", got)
	}
}

func TestDeleteObject(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "delete.txt", "delete me")

	resp := env.do(t, http.MethodDelete, "/"+env.bucket+"/delete.txt", "", nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", resp.Code, resp.Body.String())
	}
	if _, err := env.objects.GetByKey(context.Background(), env.bucket, "delete.txt"); !errors.Is(err, metadata.ErrObjectNotFound) {
		t.Fatalf("expected deleted metadata, got %v", err)
	}
}

func TestDeleteObjectIdempotent(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodDelete, "/"+env.bucket+"/missing.txt", "", nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", resp.Code, resp.Body.String())
	}
}

func TestDeleteObjectNoSuchBucket(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodDelete, "/missing/file.txt", "", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeNoSuchBucket)
}

func TestS3XMLErrors(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodGet, "/"+env.bucket+"/missing.txt", "", nil)
	if resp.Header().Get("Content-Type") != "application/xml" {
		t.Fatalf("Content-Type = %q, want application/xml", resp.Header().Get("Content-Type"))
	}

	var s3Err S3Error
	if err := xml.Unmarshal(resp.Body.Bytes(), &s3Err); err != nil {
		t.Fatalf("unmarshal XML error: %v", err)
	}
	if s3Err.Code != codeNoSuchKey {
		t.Fatalf("Code = %q, want %q", s3Err.Code, codeNoSuchKey)
	}
	if s3Err.Resource != "/"+env.bucket+"/missing.txt" {
		t.Fatalf("Resource = %q, want request path", s3Err.Resource)
	}
	if s3Err.RequestID == "" {
		t.Fatal("expected RequestId")
	}
}

func (e objectTestEnv) mustPut(t *testing.T, key, body string) {
	t.Helper()
	resp := e.do(t, http.MethodPut, "/"+e.bucket+"/"+key, body, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("put %s status = %d, want 200; body=%s", key, resp.Code, resp.Body.String())
	}
}

func (e objectTestEnv) publicReadURL(key string, ttl time.Duration) string {
	expiresAt := e.now.Add(ttl)
	path := publicread.ObjectPath(e.bucket, key)
	signature := e.signer.SignPath(path, expiresAt)
	return path + "?expires=" + strconv.FormatInt(expiresAt.Unix(), 10) + "&signature=" + signature
}

func (e objectTestEnv) do(t *testing.T, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, req)
	return rr
}

func quotedMD5(value string) string {
	sum := md5.Sum([]byte(value))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func base64MD5(value string) string {
	sum := md5.Sum([]byte(value))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func base64SHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func hexSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func assertS3ErrorCode(t *testing.T, body []byte, want string) {
	t.Helper()
	var s3Err S3Error
	if err := xml.Unmarshal(body, &s3Err); err != nil {
		t.Fatalf("unmarshal S3 error: %v; body=%s", err, string(body))
	}
	if s3Err.Code != want {
		t.Fatalf("S3 error code = %q, want %q; body=%s", s3Err.Code, want, string(body))
	}
}

func assertNoDataFiles(t *testing.T, dataDir string) {
	t.Helper()
	err := filepath.WalkDir(dataDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		t.Fatalf("unexpected data file after failed upload: %s", path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk data dir: %v", err)
	}
}
