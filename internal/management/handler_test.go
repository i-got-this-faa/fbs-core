package management_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	httpapi "github.com/i-got-this-faa/fbs/internal/http"
	"github.com/i-got-this-faa/fbs/internal/management"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := metadata.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testDeps(t *testing.T, db *sql.DB) management.Deps {
	t.Helper()
	userRepo := metadata.NewUserRepository(db)
	return management.Deps{
		Buckets: metadata.NewBucketRepository(db),
		Objects: metadata.NewObjectRepository(db),
		Users:   userRepo,
		Authenticator: &auth.ChainAuthenticator{
			Authenticators: []auth.Authenticator{
				&auth.BearerAuthenticator{Repo: userRepo},
			},
		},
	}
}

func newRouter(t *testing.T, deps management.Deps) http.Handler {
	t.Helper()
	cfg := config.Default()
	cfg.CORSAllowedOrigins = []string{"https://dashboard.example.com"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpapi.NewRouter(cfg, logger, func(r chi.Router) {
		management.RegisterRoutes(r, deps)
	})
}

// createTestUser creates a user in the DB and returns the raw Bearer token.
func createTestUser(t *testing.T, db *sql.DB, displayName, role string) string {
	t.Helper()
	issued, err := auth.IssueBearerToken()
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	sigv4, err := auth.IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4: %v", err)
	}
	user := &metadata.User{
		ID:               "user-" + displayName,
		DisplayName:      displayName,
		AccessKeyID:      issued.AccessKeyID,
		SecretHash:       issued.SecretHash,
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             role,
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := metadata.NewUserRepository(db).Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return issued.RawToken
}

// createInactiveUser creates an inactive user and returns the raw token.
func createInactiveUser(t *testing.T, db *sql.DB) string {
	t.Helper()
	issued, err := auth.IssueBearerToken()
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	sigv4, err := auth.IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4: %v", err)
	}
	user := &metadata.User{
		ID:               "user-inactive",
		DisplayName:      "Inactive",
		AccessKeyID:      issued.AccessKeyID,
		SecretHash:       issued.SecretHash,
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         false,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := metadata.NewUserRepository(db).Create(context.Background(), user); err != nil {
		t.Fatalf("create inactive user: %v", err)
	}
	return issued.RawToken
}

func createTestBucket(t *testing.T, db *sql.DB, name, ownerID string) {
	t.Helper()
	if err := metadata.NewBucketRepository(db).Create(context.Background(), &metadata.Bucket{
		Name:      name,
		OwnerID:   ownerID,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
}

func createTestObject(t *testing.T, db *sql.DB, bucket, key string, size int64) {
	t.Helper()
	if err := metadata.NewObjectRepository(db).Create(context.Background(), &metadata.Object{
		ID:          "obj-" + key,
		BucketName:  bucket,
		Key:         key,
		Size:        size,
		ETag:        "etag-" + key,
		ContentType: "application/octet-stream",
		StoragePath: bucket + "/" + key,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create object: %v", err)
	}
}

// ── CORS tests ────────────────────────────────────────────────────────────────

func TestManagement_CORS_Preflight(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodOptions, "/api/management/buckets", nil)
	req.Header.Set("Origin", "https://dashboard.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "Authorization")

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("CORS preflight: expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want dashboard origin", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodGet) {
		t.Errorf("Access-Control-Allow-Methods = %q, want GET", got)
	}
}

func TestManagement_CORS_AllowsAuthorizationHeader(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodOptions, "/api/management/buckets", nil)
	req.Header.Set("Origin", "https://dashboard.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "Authorization")

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	allowed := rr.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(strings.ToLower(allowed), "authorization") {
		t.Errorf("Authorization not in Access-Control-Allow-Headers: %q", allowed)
	}
}

// ── auth rejection tests ──────────────────────────────────────────────────────

func TestManagement_Auth_MissingToken(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	router := newRouter(t, testDeps(t, db))

	for _, path := range []string{
		"/api/management/metrics",
		"/api/management/buckets",
		"/api/management/keys",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("GET %s: expected 401 without token, got %d", path, rr.Code)
		}
		if !strings.Contains(rr.Header().Get("WWW-Authenticate"), `Bearer realm="fbs"`) {
			t.Errorf("GET %s: expected WWW-Authenticate header, got %q", path, rr.Header().Get("WWW-Authenticate"))
		}
	}
}

func TestManagement_Auth_InvalidToken(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodGet, "/api/management/buckets", nil)
	req.Header.Set("Authorization", "Bearer fbsa_bad.token")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid token, got %d", rr.Code)
	}
}

func TestManagement_Auth_InactiveUser(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createInactiveUser(t, db)
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodGet, "/api/management/buckets", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for inactive user, got %d", rr.Code)
	}
}

// ── GET /api/management/metrics ───────────────────────────────────────────────

func TestManagement_Metrics_Empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createTestUser(t, db, "alice", "admin")
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodGet, "/api/management/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["buckets"].(float64) != 0 {
		t.Errorf("buckets = %v, want 0", resp["buckets"])
	}
}

func TestManagement_Metrics_WithData(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createTestUser(t, db, "alice", "admin")
	createTestBucket(t, db, "test-bucket", "user-alice")
	createTestObject(t, db, "test-bucket", "file.txt", 1024)
	createTestObject(t, db, "test-bucket", "other.txt", 512)
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodGet, "/api/management/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["buckets"].(float64) != 1 {
		t.Errorf("buckets = %v, want 1", resp["buckets"])
	}
	if resp["objects"].(float64) != 2 {
		t.Errorf("objects = %v, want 2", resp["objects"])
	}
	if resp["storage_bytes"].(float64) != 1536 {
		t.Errorf("storage_bytes = %v, want 1536", resp["storage_bytes"])
	}
}

// ── GET /api/management/buckets ───────────────────────────────────────────────

func TestManagement_Buckets_List(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createTestUser(t, db, "alice", "admin")
	createTestBucket(t, db, "bucket-a", "user-alice")
	createTestBucket(t, db, "bucket-b", "user-alice")
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodGet, "/api/management/buckets", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Buckets []map[string]any `json:"buckets"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// user-alice was created first, but createTestUser doesn't create a bucket.
	if len(resp.Buckets) != 2 {
		t.Errorf("expected 2 buckets, got %d", len(resp.Buckets))
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// ── GET /api/management/buckets/{bucket}/objects ──────────────────────────────

func TestManagement_Objects_List(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createTestUser(t, db, "alice", "admin")
	createTestBucket(t, db, "my-bucket", "user-alice")
	createTestObject(t, db, "my-bucket", "doc.pdf", 2048)
	createTestObject(t, db, "my-bucket", "img.png", 512)
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodGet, "/api/management/buckets/my-bucket/objects", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Objects   []map[string]any `json:"objects"`
		Truncated bool             `json:"truncated"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Objects) != 2 {
		t.Errorf("expected 2 objects, got %d", len(resp.Objects))
	}
	if resp.Truncated {
		t.Error("expected truncated=false")
	}
}

func TestManagement_Objects_List_BucketNotFound(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createTestUser(t, db, "alice", "admin")
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodGet, "/api/management/buckets/no-such-bucket/objects", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// ── GET /api/management/buckets/{bucket}/objects/* ────────────────────────────

func TestManagement_Object_Get(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createTestUser(t, db, "alice", "admin")
	createTestBucket(t, db, "my-bucket", "user-alice")
	createTestObject(t, db, "my-bucket", "path/to/file.txt", 99)
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodGet, "/api/management/buckets/my-bucket/objects/path/to/file.txt", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var obj map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&obj); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if obj["key"] != "path/to/file.txt" {
		t.Errorf("key = %v, want path/to/file.txt", obj["key"])
	}
	if obj["size"].(float64) != 99 {
		t.Errorf("size = %v, want 99", obj["size"])
	}
}

func TestManagement_Object_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createTestUser(t, db, "alice", "admin")
	createTestBucket(t, db, "my-bucket", "user-alice")
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodGet, "/api/management/buckets/my-bucket/objects/missing.txt", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// ── GET /api/management/keys ──────────────────────────────────────────────────

func TestManagement_Keys_List(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createTestUser(t, db, "alice", "admin")
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodGet, "/api/management/keys", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Keys) == 0 {
		t.Fatal("expected at least one key")
	}
	// Verify no secret fields are leaked.
	for _, k := range resp.Keys {
		if _, ok := k["secret_hash"]; ok {
			t.Error("secret_hash must not be present in key list response")
		}
		if _, ok := k["sigv4_secret_key"]; ok {
			t.Error("sigv4_secret_key must not be present in key list response")
		}
	}
}

// ── POST /api/management/keys ─────────────────────────────────────────────────

func TestManagement_Keys_Create(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createTestUser(t, db, "alice", "admin")
	router := newRouter(t, testDeps(t, db))

	body, _ := json.Marshal(map[string]string{"display_name": "Bob", "role": "member"})
	req := httptest.NewRequest(http.MethodPost, "/api/management/keys", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["raw_token"] == "" || resp["raw_token"] == nil {
		t.Error("raw_token must be present in create key response")
	}
	if resp["sigv4_secret_key"] == "" || resp["sigv4_secret_key"] == nil {
		t.Error("sigv4_secret_key must be present in create key response")
	}
	if resp["id"] == "" || resp["id"] == nil {
		t.Error("id must be present in create key response")
	}
	if resp["role"] != "member" {
		t.Errorf("role = %v, want member", resp["role"])
	}
}

func TestManagement_Keys_Create_MissingDisplayName(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createTestUser(t, db, "alice", "admin")
	router := newRouter(t, testDeps(t, db))

	body, _ := json.Marshal(map[string]string{"role": "member"})
	req := httptest.NewRequest(http.MethodPost, "/api/management/keys", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// ── DELETE /api/management/keys/{id} ─────────────────────────────────────────

func TestManagement_Keys_Delete(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createTestUser(t, db, "alice", "admin")

	// Create a second user to delete.
	issued, _ := auth.IssueBearerToken()
	sigv4, _ := auth.IssueSigV4Credentials()
	victim := &metadata.User{
		ID: "user-to-delete", DisplayName: "Victim",
		AccessKeyID: issued.AccessKeyID, SecretHash: issued.SecretHash,
		SigV4AccessKeyID: sigv4.AccessKeyID, SigV4SecretKey: sigv4.SecretKey,
		Role: "member", IsActive: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := metadata.NewUserRepository(db).Create(context.Background(), victim); err != nil {
		t.Fatalf("create victim: %v", err)
	}

	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodDelete, "/api/management/keys/user-to-delete", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestManagement_Keys_Delete_NotFound(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	token := createTestUser(t, db, "alice", "admin")
	router := newRouter(t, testDeps(t, db))

	req := httptest.NewRequest(http.MethodDelete, "/api/management/keys/no-such-id", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}
