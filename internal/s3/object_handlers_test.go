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
	"github.com/i-got-this-faa/fbs/internal/storage"
)

type objectTestEnv struct {
	router  http.Handler
	objects metadata.ObjectRepository
	storage storage.DiskEngine
	bucket  string
	dataDir string
}

func newObjectTestEnv(t *testing.T) objectTestEnv {
	t.Helper()

	db, err := metadata.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	userRepo := metadata.NewUserRepository(db)
	_, user, err := auth.CreateBearerToken(context.Background(), userRepo, "Test User", "admin")
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

	objectRepo := metadata.NewObjectRepository(db)
	handlers := &ObjectHandlers{
		Buckets: bucketRepo,
		Objects: objectRepo,
		Storage: disk,
		Now:     func() time.Time { return time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC) },
		NewID:   newSequentialID(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	cfg := config.Default()
	router := httpapi.NewRouter(cfg, nil, func(r chi.Router) {
		r.Group(func(protected chi.Router) {
			protected.Use(auth.RequireAuthentication(&auth.DevAuthenticator{}, func(w http.ResponseWriter, r *http.Request, err error) {
				WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
			}))
			RegisterObjectRoutes(protected, handlers)
		})
	})

	return objectTestEnv{
		router:  router,
		objects: objectRepo,
		storage: disk,
		bucket:  bucketName,
		dataDir: dataDir,
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
