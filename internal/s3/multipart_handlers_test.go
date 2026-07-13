package s3

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	httpapi "github.com/i-got-this-faa/fbs/internal/http"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

func TestCreateMultipartUpload(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPost, "/"+env.bucket+"/large.zip?uploads", "", nil)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	var result InitiateMultipartUploadResult
	if err := xml.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if result.Bucket != env.bucket {
		t.Errorf("Bucket = %q, want %q", result.Bucket, env.bucket)
	}
	if result.Key != "large.zip" {
		t.Errorf("Key = %q, want large.zip", result.Key)
	}
	if result.UploadID == "" {
		t.Fatal("expected non-empty UploadID")
	}
}

func TestCreateMultipartUploadNoSuchBucket(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPost, "/missing/large.zip?uploads", "", nil)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeNoSuchBucket)
}

func TestUploadPart(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")

	body := "part one content"
	resp := env.do(t, http.MethodPut, fmt.Sprintf("/%s/large.zip?partNumber=1&uploadId=%s", env.bucket, uploadID), body, nil)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	wantETag := quotedMD5(body)
	if got := resp.Header().Get("ETag"); got != wantETag {
		t.Fatalf("ETag = %q, want %q", got, wantETag)
	}

	parts, err := env.multipartUploads.ListParts(context.Background(), uploadID)
	if err != nil {
		t.Fatalf("list parts: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].PartNumber != 1 {
		t.Errorf("PartNumber = %d, want 1", parts[0].PartNumber)
	}
	if parts[0].Size != int64(len(body)) {
		t.Errorf("Size = %d, want %d", parts[0].Size, len(body))
	}
}

func TestUploadPartNoSuchUpload(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPut, fmt.Sprintf("/%s/large.zip?partNumber=1&uploadId=nonexistent", env.bucket), "data", nil)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeNoSuchUpload)
}

func TestUploadPartBadDigest(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")

	resp := env.do(t, http.MethodPut, fmt.Sprintf("/%s/large.zip?partNumber=1&uploadId=%s", env.bucket, uploadID), "data", map[string]string{
		"Content-MD5": base64MD5("different"),
	})

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeBadDigest)
}

func TestCompleteMultipartUpload(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")

	part1Body := "part one content"
	part1ETag := env.mustUploadPart(t, uploadID, "large.zip", 1, part1Body)

	part2Body := "part two content"
	part2ETag := env.mustUploadPart(t, uploadID, "large.zip", 2, part2Body)

	completeXML := fmt.Sprintf(`<CompleteMultipartUpload>
		<Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part>
		<Part><PartNumber>2</PartNumber><ETag>%s</ETag></Part>
	</CompleteMultipartUpload>`, part1ETag, part2ETag)

	resp := env.do(t, http.MethodPost, fmt.Sprintf("/%s/large.zip?uploadId=%s", env.bucket, uploadID), completeXML, nil)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	var result CompleteMultipartUploadResult
	if err := xml.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if result.Bucket != env.bucket {
		t.Errorf("Bucket = %q, want %q", result.Bucket, env.bucket)
	}
	if result.Key != "large.zip" {
		t.Errorf("Key = %q, want large.zip", result.Key)
	}
	if result.ETag == "" {
		t.Fatal("expected non-empty ETag")
	}

	// Verify object exists.
	obj, err := env.objects.GetByKey(context.Background(), env.bucket, "large.zip")
	if err != nil {
		t.Fatalf("get object: %v", err)
	}
	wantSize := int64(len(part1Body) + len(part2Body))
	if obj.Size != wantSize {
		t.Errorf("Size = %d, want %d", obj.Size, wantSize)
	}

	// Verify upload is gone.
	if _, err := env.multipartUploads.GetByID(context.Background(), uploadID); !errors.Is(err, metadata.ErrMultipartUploadNotFound) {
		t.Fatalf("expected upload to be deleted, got %v", err)
	}
}

func TestCompleteMultipartUploadInvalidPartOrder(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")

	part2ETag := env.mustUploadPart(t, uploadID, "large.zip", 2, "part two")
	part1ETag := env.mustUploadPart(t, uploadID, "large.zip", 1, "part one")

	// Parts out of order in request.
	completeXML := fmt.Sprintf(`<CompleteMultipartUpload>
		<Part><PartNumber>2</PartNumber><ETag>%s</ETag></Part>
		<Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part>
	</CompleteMultipartUpload>`, part2ETag, part1ETag)

	resp := env.do(t, http.MethodPost, fmt.Sprintf("/%s/large.zip?uploadId=%s", env.bucket, uploadID), completeXML, nil)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeInvalidPartOrder)
}

func TestCompleteMultipartUploadInvalidPart(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")

	part1ETag := env.mustUploadPart(t, uploadID, "large.zip", 1, "part one")

	// Request includes part 2 which was never uploaded.
	completeXML := fmt.Sprintf(`<CompleteMultipartUpload>
		<Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part>
		<Part><PartNumber>2</PartNumber><ETag>"d41d8cd98f00b204e9800998ecf8427e"</ETag></Part>
	</CompleteMultipartUpload>`, part1ETag)

	resp := env.do(t, http.MethodPost, fmt.Sprintf("/%s/large.zip?uploadId=%s", env.bucket, uploadID), completeXML, nil)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeInvalidPart)
}

func TestCompleteMultipartUploadNoSuchUpload(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPost, "/"+env.bucket+"/large.zip?uploadId=nonexistent", "<CompleteMultipartUpload></CompleteMultipartUpload>", nil)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeNoSuchUpload)
}

func TestAbortMultipartUpload(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")
	env.mustUploadPart(t, uploadID, "large.zip", 1, "part one")

	resp := env.do(t, http.MethodDelete, fmt.Sprintf("/%s/large.zip?uploadId=%s", env.bucket, uploadID), "", nil)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", resp.Code, resp.Body.String())
	}

	if _, err := env.multipartUploads.GetByID(context.Background(), uploadID); !errors.Is(err, metadata.ErrMultipartUploadNotFound) {
		t.Fatalf("expected upload to be deleted, got %v", err)
	}
}

func TestAbortMultipartUploadNoSuchUpload(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodDelete, "/"+env.bucket+"/large.zip?uploadId=nonexistent", "", nil)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeNoSuchUpload)
}

func TestCompleteMultipartUploadMismatchedETag(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")

	env.mustUploadPart(t, uploadID, "large.zip", 1, "part one")

	completeXML := `<CompleteMultipartUpload>
		<Part><PartNumber>1</PartNumber><ETag>"d41d8cd98f00b204e9800998ecf8427e"</ETag></Part>
	</CompleteMultipartUpload>`

	resp := env.do(t, http.MethodPost, fmt.Sprintf("/%s/large.zip?uploadId=%s", env.bucket, uploadID), completeXML, nil)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeInvalidPart)
}

func TestUploadPartOverwrite(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")

	firstETag := env.mustUploadPart(t, uploadID, "large.zip", 1, "first content")
	secondETag := env.mustUploadPart(t, uploadID, "large.zip", 1, "second content")

	if firstETag == secondETag {
		t.Fatal("expected different ETags after overwrite")
	}

	parts, err := env.multipartUploads.ListParts(context.Background(), uploadID)
	if err != nil {
		t.Fatalf("list parts: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].Size != int64(len("second content")) {
		t.Errorf("Size = %d, want %d", parts[0].Size, len("second content"))
	}

	// Complete with the new ETag.
	completeXML := fmt.Sprintf(`<CompleteMultipartUpload>
		<Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part>
	</CompleteMultipartUpload>`, secondETag)

	resp := env.do(t, http.MethodPost, fmt.Sprintf("/%s/large.zip?uploadId=%s", env.bucket, uploadID), completeXML, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("complete status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	// Verify assembled object content.
	getResp := env.do(t, http.MethodGet, "/"+env.bucket+"/large.zip", "", nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", getResp.Code, getResp.Body.String())
	}
	if getResp.Body.String() != "second content" {
		t.Fatalf("body = %q, want second content", getResp.Body.String())
	}
}

func TestUploadPartOverwriteDeletesOldFile(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")

	// Upload first part.
	firstETag := env.mustUploadPart(t, uploadID, "large.zip", 1, "first content")
	parts, _ := env.multipartUploads.ListParts(context.Background(), uploadID)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	oldPath := parts[0].StoragePath

	// Upload second part (overwrite).
	secondETag := env.mustUploadPart(t, uploadID, "large.zip", 1, "second content")
	if firstETag == secondETag {
		t.Fatal("expected different ETags")
	}

	// The old part file should have been deleted.
	_, err := env.storage.Read(context.Background(), oldPath)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected old part file to be deleted, got err=%v", err)
	}
}

func TestMultipartETag(t *testing.T) {
	t.Parallel()

	part1 := md5Hash([]byte("hello"))
	part2 := md5Hash([]byte("world"))
	etag, err := MultipartETag([]string{part1, part2})
	if err != nil {
		t.Fatalf("MultipartETag error: %v", err)
	}

	// ETag should end with -2
	if !strings.HasSuffix(etag, "-2") {
		t.Fatalf("ETag = %q, expected suffix -2", etag)
	}
}

func (e objectTestEnv) mustCreateMultipartUpload(t *testing.T, key string) string {
	t.Helper()
	return e.mustCreateMultipartUploadWithHeaders(t, key, nil)
}

func (e objectTestEnv) mustCreateMultipartUploadWithHeaders(t *testing.T, key string, headers map[string]string) string {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/"+e.bucket+"/"+key+"?uploads", "", headers)
	if resp.Code != http.StatusOK {
		t.Fatalf("create multipart upload status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	var result InitiateMultipartUploadResult
	if err := xml.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	return result.UploadID
}

func (e objectTestEnv) mustUploadPart(t *testing.T, uploadID, key string, partNumber int, body string) string {
	t.Helper()
	path := fmt.Sprintf("/%s/%s?partNumber=%d&uploadId=%s", e.bucket, key, partNumber, uploadID)
	resp := e.do(t, http.MethodPut, path, body, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("upload part %d status = %d, want 200; body=%s", partNumber, resp.Code, resp.Body.String())
	}
	return resp.Header().Get("ETag")
}

func md5Hash(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

func TestUploadPartWrongBucketKey(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")

	// Try uploading to a different key with the same uploadID.
	resp := env.do(t, http.MethodPut, fmt.Sprintf("/%s/other.zip?partNumber=1&uploadId=%s", env.bucket, uploadID), "data", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeNoSuchUpload)
}

func TestAbortMultipartUploadWrongBucketKey(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")

	// Try aborting via a different key with the same uploadID.
	resp := env.do(t, http.MethodDelete, fmt.Sprintf("/%s/other.zip?uploadId=%s", env.bucket, uploadID), "", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeNoSuchUpload)
}

func TestDispatchPutIncompleteMultipartParams(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")

	// uploadId without partNumber should error, not PutObject.
	resp := env.do(t, http.MethodPut, fmt.Sprintf("/%s/large.zip?uploadId=%s", env.bucket, uploadID), "data", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeInvalidRequest)

	// partNumber without uploadId should error, not PutObject.
	resp = env.do(t, http.MethodPut, fmt.Sprintf("/%s/large.zip?partNumber=1", env.bucket), "data", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeInvalidRequest)
}

func TestDispatchDeleteEmptyUploadId(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "exists.txt", "hello")

	// DELETE with uploadId present but empty should error, not delete object.
	resp := env.do(t, http.MethodDelete, "/"+env.bucket+"/exists.txt?uploadId=", "", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeInvalidRequest)

	// Verify object still exists.
	obj, err := env.objects.GetByKey(context.Background(), env.bucket, "exists.txt")
	if err != nil {
		t.Fatalf("object should still exist: %v", err)
	}
	if obj.Size != 5 {
		t.Fatalf("object size = %d, want 5", obj.Size)
	}
}

func TestCreateMultipartUploadInvalidKey(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPost, "/"+env.bucket+"/../escape.zip?uploads", "", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeInvalidRequest)
}

func TestCompleteMultipartUploadUsesInitiationContentType(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUploadWithHeaders(t, "large.zip", map[string]string{
		"Content-Type": "image/png",
	})

	etag := env.mustUploadPart(t, uploadID, "large.zip", 1, "part one")
	completeXML := fmt.Sprintf(`<CompleteMultipartUpload>
		<Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part>
	</CompleteMultipartUpload>`, etag)

	resp := env.do(t, http.MethodPost, fmt.Sprintf("/%s/large.zip?uploadId=%s", env.bucket, uploadID), completeXML, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("complete status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	obj, err := env.objects.GetByKey(context.Background(), env.bucket, "large.zip")
	if err != nil {
		t.Fatalf("get object: %v", err)
	}
	if obj.ContentType != "image/png" {
		t.Fatalf("ContentType = %q, want image/png", obj.ContentType)
	}
}

func TestCompleteMultipartUploadEnforcesMinPartSize(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	// newObjectTestEnv sets MinPartSize=1; reset to default for this test.
	// We verify the default 5 MiB rule by using a handler with MinPartSize=0.
	// Build a minimal env with default MinPartSize.
	db, err := metadata.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	userRepo := metadata.NewUserRepository(db)
	_, _, user, err := auth.CreateBearerToken(context.Background(), userRepo, "Test User", "admin")
	if err != nil {
		t.Fatalf("create bearer token: %v", err)
	}

	bucketRepo := metadata.NewBucketRepository(db)
	if err := bucketRepo.Create(context.Background(), &metadata.Bucket{
		Name:      env.bucket,
		OwnerID:   user.ID,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	disk, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	objectRepo := metadata.NewObjectRepository(db)
	multipartRepo := metadata.NewMultipartUploadRepository(db)
	grantRepo := metadata.NewGrantRepository(db)
	handlers := &ObjectHandlers{
		Users:            userRepo,
		Buckets:          bucketRepo,
		Objects:          objectRepo,
		MultipartUploads: multipartRepo,
		Grants:           grantRepo,
		Authz:            NewAuthzEvaluator(grantRepo),
		Storage:          disk,
		Now:              func() time.Time { return time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC) },
		NewID:            newSequentialID(),
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		// MinPartSize left at 0 → default 5 MiB
	}

	cfg := config.Default()
	router := httpapi.NewRouter(cfg, nil, func(r chi.Router) {
		r.Group(func(protected chi.Router) {
			protected.Use(auth.RequireAuthentication(&auth.DevAuthenticator{}, func(w http.ResponseWriter, r *http.Request, err error) {
				WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
			}))
			RegisterBucketRoutes(protected, handlers)
			RegisterObjectRoutes(protected, handlers)
		})
	})

	testEnv := objectTestEnv{
		router:           router,
		users:            userRepo,
		buckets:          bucketRepo,
		objects:          objectRepo,
		multipartUploads: multipartRepo,
		storage:          disk,
		bucket:           env.bucket,
		dataDir:          t.TempDir(),
		userID:           user.ID,
	}

	uploadID := testEnv.mustCreateMultipartUpload(t, "large.zip")

	// Upload two tiny parts.
	etag1 := testEnv.mustUploadPart(t, uploadID, "large.zip", 1, "small1")
	etag2 := testEnv.mustUploadPart(t, uploadID, "large.zip", 2, "small2")

	completeXML := fmt.Sprintf(`<CompleteMultipartUpload>
		<Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part>
		<Part><PartNumber>2</PartNumber><ETag>%s</ETag></Part>
	</CompleteMultipartUpload>`, etag1, etag2)

	resp := testEnv.do(t, http.MethodPost, fmt.Sprintf("/%s/large.zip?uploadId=%s", testEnv.bucket, uploadID), completeXML, nil)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeEntityTooSmall)
}

func TestCompleteMultipartUploadValidationFailureResetsClaim(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	uploadID := env.mustCreateMultipartUpload(t, "large.zip")

	etag := env.mustUploadPart(t, uploadID, "large.zip", 1, "part one")

	// Send an invalid complete request (parts out of order).
	completeXML := fmt.Sprintf(`<CompleteMultipartUpload>
		<Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part>
		<Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part>
	</CompleteMultipartUpload>`, etag, etag)

	resp := env.do(t, http.MethodPost, fmt.Sprintf("/%s/large.zip?uploadId=%s", env.bucket, uploadID), completeXML, nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}

	// The upload should still be active and listable.
	upload, err := env.multipartUploads.GetByID(context.Background(), uploadID)
	if err != nil {
		t.Fatalf("upload should still exist: %v", err)
	}
	if upload.Status != "active" {
		t.Fatalf("upload status = %q, want active", upload.Status)
	}

	// A subsequent valid complete should still work.
	completeXML = fmt.Sprintf(`<CompleteMultipartUpload>
		<Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part>
	</CompleteMultipartUpload>`, etag)
	resp = env.do(t, http.MethodPost, fmt.Sprintf("/%s/large.zip?uploadId=%s", env.bucket, uploadID), completeXML, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("second complete status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
}

func TestStaleMultipartCleanup(t *testing.T) {
	t.Parallel()

	db, err := metadata.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ownerID := uuid.NewString()
	userRepo := metadata.NewUserRepository(db)
	if err := userRepo.Create(context.Background(), &metadata.User{
		ID:          ownerID,
		DisplayName: "Test",
		AccessKeyID: "ak-" + uuid.NewString()[:8],
		SecretHash:  "hash",
		Role:        "admin",
		IsActive:    true,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	bucketRepo := metadata.NewBucketRepository(db)
	bucketName := "test-bucket"
	if err := bucketRepo.Create(context.Background(), &metadata.Bucket{
		Name:      bucketName,
		OwnerID:   ownerID,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	dataDir := t.TempDir()
	store, err := storage.New(dataDir)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	multipartRepo := metadata.NewMultipartUploadRepository(db)

	// Create a stale upload.
	staleUpload := &metadata.MultipartUpload{
		ID:         uuid.NewString(),
		BucketName: bucketName,
		Key:        "stale-key",
		CreatedAt:  time.Now().UTC().Add(-48 * time.Hour),
	}
	if err := multipartRepo.Create(context.Background(), staleUpload); err != nil {
		t.Fatalf("create stale upload: %v", err)
	}

	// Add a part to the stale upload so we can verify disk cleanup.
	_, _, err = store.WritePart(context.Background(), staleUpload.ID, 1, strings.NewReader("stale part"))
	if err != nil {
		t.Fatalf("write stale part: %v", err)
	}

	// Create a recent upload.
	recentUpload := &metadata.MultipartUpload{
		ID:         uuid.NewString(),
		BucketName: bucketName,
		Key:        "recent-key",
		CreatedAt:  time.Now().UTC(),
	}
	if err := multipartRepo.Create(context.Background(), recentUpload); err != nil {
		t.Fatalf("create recent upload: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go StaleMultipartCleanup(ctx, multipartRepo, store, 24*time.Hour, 10*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))

	<-ctx.Done()

	if _, err := multipartRepo.GetByID(context.Background(), staleUpload.ID); !errors.Is(err, metadata.ErrMultipartUploadNotFound) {
		t.Fatalf("expected stale upload to be deleted, got %v", err)
	}

	if _, err := multipartRepo.GetByID(context.Background(), recentUpload.ID); err != nil {
		t.Fatalf("expected recent upload to exist: %v", err)
	}
}
