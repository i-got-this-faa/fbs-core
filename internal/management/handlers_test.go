package management_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	httpapi "github.com/i-got-this-faa/fbs/internal/http"
	"github.com/i-got-this-faa/fbs/internal/management"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

type managementTestEnv struct {
	router       http.Handler
	db           *sql.DB
	adminToken   string
	adminUserID  string
	memberToken  string
	memberUserID string
	memberSigV4  auth.SigV4Credentials
}

func TestManagementMetrics(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	resp := env.do(t, http.MethodGet, "/api/management/metrics", env.adminToken, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		BucketCount      int64 `json:"bucket_count"`
		ObjectCount      int64 `json:"object_count"`
		TotalObjectBytes int64 `json:"total_object_bytes"`
		UserCount        int64 `json:"user_count"`
		ActiveUserCount  int64 `json:"active_user_count"`
	}
	decodeResponse(t, resp, &body)

	if body.BucketCount != 2 || body.ObjectCount != 4 || body.TotalObjectBytes != 100 || body.UserCount != 2 || body.ActiveUserCount != 2 {
		t.Fatalf("metrics = %+v, want bucket=2 object=4 bytes=100 users=2 active=2", body)
	}
}

func TestManagementListBuckets(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	resp := env.do(t, http.MethodGet, "/api/management/buckets", env.adminToken, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Buckets []struct {
			Name             string `json:"name"`
			OwnerID          string `json:"owner_id"`
			CreatedAt        string `json:"created_at"`
			ObjectCount      int64  `json:"object_count"`
			TotalObjectBytes int64  `json:"total_object_bytes"`
		} `json:"buckets"`
	}
	decodeResponse(t, resp, &body)

	if len(body.Buckets) != 2 {
		t.Fatalf("len(buckets) = %d, want 2", len(body.Buckets))
	}
	photos := body.Buckets[0]
	if photos.Name != "photos" || photos.ObjectCount != 3 || photos.TotalObjectBytes != 60 {
		t.Fatalf("photos summary = %+v, want name=photos count=3 bytes=60", photos)
	}
	if photos.OwnerID == "" || photos.CreatedAt == "" {
		t.Fatalf("photos summary missing owner/created_at: %+v", photos)
	}
}

func TestManagementListObjectsSupportsPrefixDelimiterCursorAndLimit(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	delimited := env.do(t, http.MethodGet, "/api/management/buckets/photos/objects?prefix=2026/&delimiter=/&limit=100", env.adminToken, nil)
	defer delimited.Body.Close()

	if delimited.StatusCode != http.StatusOK {
		t.Fatalf("delimited status = %d, want %d", delimited.StatusCode, http.StatusOK)
	}

	var delimitedBody struct {
		Bucket      string `json:"bucket"`
		Prefix      string `json:"prefix"`
		Delimiter   string `json:"delimiter"`
		Limit       int    `json:"limit"`
		IsTruncated bool   `json:"is_truncated"`
		NextCursor  string `json:"next_cursor"`
		Objects     []struct {
			Key         string `json:"key"`
			Size        int64  `json:"size"`
			ETag        string `json:"etag"`
			ContentType string `json:"content_type"`
			CreatedAt   string `json:"created_at"`
			UpdatedAt   string `json:"updated_at"`
		} `json:"objects"`
		CommonPrefixes []string `json:"common_prefixes"`
	}
	decodeResponse(t, delimited, &delimitedBody)

	if delimitedBody.Bucket != "photos" || delimitedBody.Prefix != "2026/" || delimitedBody.Delimiter != "/" || delimitedBody.Limit != 100 {
		t.Fatalf("list metadata = %+v", delimitedBody)
	}
	if len(delimitedBody.Objects) != 1 || delimitedBody.Objects[0].Key != "2026/image.jpg" {
		t.Fatalf("objects = %+v, want 2026/image.jpg only", delimitedBody.Objects)
	}
	if len(delimitedBody.CommonPrefixes) != 1 || delimitedBody.CommonPrefixes[0] != "2026/raw/" {
		t.Fatalf("common_prefixes = %+v, want 2026/raw/", delimitedBody.CommonPrefixes)
	}

	firstPage := env.do(t, http.MethodGet, "/api/management/buckets/photos/objects?limit=1", env.adminToken, nil)
	defer firstPage.Body.Close()

	var firstPageBody struct {
		IsTruncated bool   `json:"is_truncated"`
		NextCursor  string `json:"next_cursor"`
		Objects     []struct {
			Key string `json:"key"`
		} `json:"objects"`
	}
	decodeResponse(t, firstPage, &firstPageBody)
	if !firstPageBody.IsTruncated || firstPageBody.NextCursor == "" || len(firstPageBody.Objects) != 1 {
		t.Fatalf("first page = %+v, want truncated single object with cursor", firstPageBody)
	}

	secondPage := env.do(t, http.MethodGet, "/api/management/buckets/photos/objects?limit=1&cursor="+firstPageBody.NextCursor, env.adminToken, nil)
	defer secondPage.Body.Close()

	var secondPageBody struct {
		Objects []struct {
			Key string `json:"key"`
		} `json:"objects"`
	}
	decodeResponse(t, secondPage, &secondPageBody)
	if len(secondPageBody.Objects) != 1 || secondPageBody.Objects[0].Key <= firstPageBody.Objects[0].Key {
		t.Fatalf("second page = %+v, want key after %q", secondPageBody, firstPageBody.Objects[0].Key)
	}
}

func TestManagementObjectDetailSupportsSlashKeys(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	resp := env.do(t, http.MethodGet, "/api/management/buckets/photos/objects/2026/image.jpg", env.adminToken, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Object struct {
			Key         string `json:"key"`
			Bucket      string `json:"bucket"`
			Size        int64  `json:"size"`
			ETag        string `json:"etag"`
			ContentType string `json:"content_type"`
			CreatedAt   string `json:"created_at"`
			UpdatedAt   string `json:"updated_at"`
			StoragePath string `json:"storage_path"`
		} `json:"object"`
	}
	decodeResponse(t, resp, &body)

	if body.Object.Key != "2026/image.jpg" || body.Object.Bucket != "photos" || body.Object.StoragePath != "" {
		t.Fatalf("object detail = %+v", body.Object)
	}
}

func TestManagementMissingBucketAndObjectReturnNotFound(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	missingBucket := env.do(t, http.MethodGet, "/api/management/buckets/missing/objects", env.adminToken, nil)
	defer missingBucket.Body.Close()
	if missingBucket.StatusCode != http.StatusNotFound {
		t.Fatalf("missing bucket status = %d, want %d", missingBucket.StatusCode, http.StatusNotFound)
	}

	missingObject := env.do(t, http.MethodGet, "/api/management/buckets/photos/objects/missing.jpg", env.adminToken, nil)
	defer missingObject.Body.Close()
	if missingObject.StatusCode != http.StatusNotFound {
		t.Fatalf("missing object status = %d, want %d", missingObject.StatusCode, http.StatusNotFound)
	}
}

func TestManagementListKeysExcludesSecrets(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	resp := env.do(t, http.MethodGet, "/api/management/keys", env.adminToken, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	bodyBytes := readBody(t, resp)
	bodyText := string(bodyBytes)
	if strings.Contains(bodyText, "secret_hash") || strings.Contains(bodyText, env.memberToken) || strings.Contains(bodyText, env.memberSigV4.SecretKey) {
		t.Fatalf("keys response exposed a secret: %s", bodyText)
	}

	var body struct {
		Keys []struct {
			ID               string `json:"id"`
			DisplayName      string `json:"display_name"`
			AccessKeyID      string `json:"access_key_id"`
			SigV4AccessKeyID string `json:"sigv4_access_key_id"`
			Role             string `json:"role"`
			IsActive         bool   `json:"is_active"`
			CreatedAt        string `json:"created_at"`
			UpdatedAt        string `json:"updated_at"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Keys) != 2 {
		t.Fatalf("len(keys) = %d, want 2", len(body.Keys))
	}
}

func TestManagementCreateKeyReturnsGeneratedSecretsOnce(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	payload := strings.NewReader(`{"display_name":" Dashboard Admin ","role":"admin"}`)

	resp := env.do(t, http.MethodPost, "/api/management/keys", env.adminToken, payload)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := resp.Header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}

	var body struct {
		Key struct {
			ID               string `json:"id"`
			DisplayName      string `json:"display_name"`
			AccessKeyID      string `json:"access_key_id"`
			SigV4AccessKeyID string `json:"sigv4_access_key_id"`
			Role             string `json:"role"`
			IsActive         bool   `json:"is_active"`
		} `json:"key"`
		BearerToken string `json:"bearer_token"`
		SigV4       struct {
			AccessKeyID string `json:"access_key_id"`
			SecretKey   string `json:"secret_key"`
		} `json:"sigv4"`
	}
	decodeResponse(t, resp, &body)

	if body.Key.DisplayName != "Dashboard Admin" || body.Key.Role != "admin" || !body.Key.IsActive {
		t.Fatalf("created key = %+v", body.Key)
	}
	if body.BearerToken == "" || body.SigV4.AccessKeyID == "" || body.SigV4.SecretKey == "" {
		t.Fatalf("create key did not return generated credentials: %+v", body)
	}

	listResp := env.do(t, http.MethodGet, "/api/management/keys", env.adminToken, nil)
	defer listResp.Body.Close()
	listText := string(readBody(t, listResp))
	if strings.Contains(listText, body.BearerToken) || strings.Contains(listText, body.SigV4.SecretKey) {
		t.Fatalf("list keys exposed newly generated secret: %s", listText)
	}
}

func TestManagementInvalidCreateKeyPayloadReturnsBadRequest(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	cases := []string{
		`{"display_name":"   ","role":"admin"}`,
		`{"display_name":"Bad Role","role":"owner"}`,
	}
	for _, payload := range cases {
		resp := env.do(t, http.MethodPost, "/api/management/keys", env.adminToken, strings.NewReader(payload))
		if resp.StatusCode != http.StatusBadRequest {
			resp.Body.Close()
			t.Fatalf("payload %s status = %d, want %d", payload, resp.StatusCode, http.StatusBadRequest)
		}
		resp.Body.Close()
	}
}

func TestManagementDeleteKey(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	resp := env.do(t, http.MethodDelete, "/api/management/keys/"+env.memberUserID, env.adminToken, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	if _, err := metadata.NewUserRepository(env.db).GetByID(context.Background(), env.memberUserID); !errorsIsUserNotFound(err) {
		t.Fatalf("deleted key lookup error = %v, want ErrUserNotFound", err)
	}

	missing := env.do(t, http.MethodDelete, "/api/management/keys/"+env.memberUserID, env.adminToken, nil)
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing status = %d, want %d", missing.StatusCode, http.StatusNotFound)
	}
}

func TestManagementAuthRequiresAdmin(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	missingAuth := env.do(t, http.MethodGet, "/api/management/metrics", "", nil)
	defer missingAuth.Body.Close()
	if missingAuth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing auth status = %d, want %d", missingAuth.StatusCode, http.StatusUnauthorized)
	}
	if missingAuth.Header.Get("WWW-Authenticate") != `Bearer realm="fbs"` {
		t.Fatalf("WWW-Authenticate = %q", missingAuth.Header.Get("WWW-Authenticate"))
	}

	member := env.do(t, http.MethodGet, "/api/management/metrics", env.memberToken, nil)
	defer member.Body.Close()
	if member.StatusCode != http.StatusForbidden {
		t.Fatalf("member status = %d, want %d", member.StatusCode, http.StatusForbidden)
	}
}

func TestManagementCORSPreflight(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/management/keys", nil)
	req.Header.Set("Origin", "https://dashboard.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	rr := httptest.NewRecorder()

	env.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPost) {
		t.Fatalf("Access-Control-Allow-Methods = %q, want POST", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") || !strings.Contains(got, "Content-Type") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want Authorization and Content-Type", got)
	}
}

func newManagementTestEnv(t *testing.T) managementTestEnv {
	t.Helper()

	db, err := metadata.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
	})

	ctx := context.Background()
	userRepo := metadata.NewUserRepository(db)
	adminToken, _, adminUser, err := auth.CreateBearerToken(ctx, userRepo, "Admin User", "admin")
	if err != nil {
		t.Fatalf("create admin token: %v", err)
	}
	memberToken, memberSigV4, memberUser, err := auth.CreateBearerToken(ctx, userRepo, "Member User", "member")
	if err != nil {
		t.Fatalf("create member token: %v", err)
	}

	seedManagementHandlerData(t, ctx, db, adminUser.ID)

	bucketRepo := metadata.NewBucketRepository(db)
	objectRepo := metadata.NewObjectRepository(db)
	handlers := &management.Handlers{
		Management: metadata.NewManagementRepository(db),
		Buckets:    bucketRepo,
		Objects:    objectRepo,
		Users:      userRepo,
	}
	authChain := &auth.ChainAuthenticator{
		Authenticators: []auth.Authenticator{
			&auth.BearerAuthenticator{Repo: userRepo},
		},
	}

	cfg := config.Default()
	cfg.CORSAllowedOrigins = []string{"https://dashboard.example.com"}
	router := httpapi.NewRouter(cfg, nil, func(r chi.Router) {
		r.Route("/api/management", func(managementRoutes chi.Router) {
			managementRoutes.Use(auth.RequireAuthentication(authChain, management.WriteAuthError))
			managementRoutes.Use(auth.RequireRole("admin", management.WriteAuthError))
			management.RegisterRoutes(managementRoutes, handlers)
		})
	})

	return managementTestEnv{
		router:       router,
		db:           db,
		adminToken:   adminToken.RawToken,
		adminUserID:  adminUser.ID,
		memberToken:  memberToken.RawToken,
		memberUserID: memberUser.ID,
		memberSigV4:  memberSigV4,
	}
}

func seedManagementHandlerData(t *testing.T, ctx context.Context, db *sql.DB, ownerID string) {
	t.Helper()

	bucketRepo := metadata.NewBucketRepository(db)
	objectRepo := metadata.NewObjectRepository(db)
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)

	buckets := []*metadata.Bucket{
		{Name: "photos", OwnerID: ownerID, CreatedAt: now},
		{Name: "docs", OwnerID: ownerID, CreatedAt: now.Add(time.Second)},
	}
	for _, bucket := range buckets {
		if err := bucketRepo.Create(ctx, bucket); err != nil {
			t.Fatalf("create bucket: %v", err)
		}
	}

	objects := []*metadata.Object{
		{ID: uuid.NewString(), BucketName: "photos", Key: "2026/image.jpg", Size: 10, ETag: "etag-image", ContentType: "image/jpeg", StoragePath: "hidden/image", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), BucketName: "photos", Key: "2026/raw/a.nef", Size: 20, ETag: "etag-raw-a", ContentType: "image/x-nikon-nef", StoragePath: "hidden/raw-a", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), BucketName: "photos", Key: "2026/raw/b.nef", Size: 30, ETag: "etag-raw-b", ContentType: "image/x-nikon-nef", StoragePath: "hidden/raw-b", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), BucketName: "docs", Key: "readme.txt", Size: 40, ETag: "etag-readme", ContentType: "text/plain", StoragePath: "hidden/readme", CreatedAt: now, UpdatedAt: now},
	}
	for _, obj := range objects {
		if err := objectRepo.Create(ctx, obj); err != nil {
			t.Fatalf("create object: %v", err)
		}
	}
}

func (e managementTestEnv) do(t *testing.T, method, path, token string, body *strings.Reader) *http.Response {
	t.Helper()

	var requestBody *strings.Reader
	if body == nil {
		requestBody = strings.NewReader("")
	} else {
		requestBody = body
	}
	req := httptest.NewRequest(method, path, requestBody)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, req)
	return rr.Result()
}

func decodeResponse(t *testing.T, resp *http.Response, dst interface{}) {
	t.Helper()

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()

	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read response: %v", err)
	}
	return buf.Bytes()
}

func errorsIsUserNotFound(err error) bool {
	return err == metadata.ErrUserNotFound
}
