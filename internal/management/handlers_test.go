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
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	httpapi "github.com/i-got-this-faa/fbs/internal/http"
	"github.com/i-got-this-faa/fbs/internal/management"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/publicread"
	"github.com/i-got-this-faa/fbs/internal/s3"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

type managementTestEnv struct {
	router       http.Handler
	db           *sql.DB
	adminToken   string
	adminSigV4   auth.SigV4Credentials
	adminUserID  string
	memberToken  string
	memberUserID string
	memberSigV4  auth.SigV4Credentials
	storage      storage.DiskEngine
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

func TestManagementDeleteBucketRemovesObjects(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	resp := env.do(t, http.MethodDelete, "/api/management/buckets/photos", env.adminToken, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	if _, err := metadata.NewBucketRepository(env.db).GetByName(context.Background(), "photos"); !errorsIsBucketNotFound(err) {
		t.Fatalf("deleted bucket lookup error = %v, want ErrBucketNotFound", err)
	}

	if _, err := metadata.NewObjectRepository(env.db).GetByKey(context.Background(), "photos", "2026/image.jpg"); !errorsIsObjectNotFound(err) {
		t.Fatalf("deleted object lookup error = %v, want ErrObjectNotFound", err)
	}

	missing := env.do(t, http.MethodDelete, "/api/management/buckets/photos", env.adminToken, nil)
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing status = %d, want %d", missing.StatusCode, http.StatusNotFound)
	}
}

func TestManagementGetBucket(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	resp := env.do(t, http.MethodGet, "/api/management/buckets/photos", env.adminToken, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Bucket struct {
			Name             string `json:"name"`
			OwnerID          string `json:"owner_id"`
			CreatedAt        string `json:"created_at"`
			ObjectCount      int64  `json:"object_count"`
			TotalObjectBytes int64  `json:"total_object_bytes"`
		} `json:"bucket"`
	}
	decodeResponse(t, resp, &body)
	if body.Bucket.Name != "photos" || body.Bucket.ObjectCount != 3 || body.Bucket.TotalObjectBytes != 60 {
		t.Fatalf("bucket = %+v, want photos count=3 bytes=60", body.Bucket)
	}
	if body.Bucket.OwnerID == "" || body.Bucket.CreatedAt == "" {
		t.Fatalf("bucket missing owner/created_at: %+v", body.Bucket)
	}
}

func TestManagementEmptyBucketRemovesObjectsButKeepsBucket(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	resp := env.do(t, http.MethodPost, "/api/management/buckets/photos/empty", env.adminToken, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if _, err := metadata.NewBucketRepository(env.db).GetByName(context.Background(), "photos"); err != nil {
		t.Fatalf("bucket should remain: %v", err)
	}
	objects, truncated, err := metadata.NewObjectRepository(env.db).List(context.Background(), "photos", "", "", 10)
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}
	if truncated || len(objects) != 0 {
		t.Fatalf("objects = %v truncated=%v, want empty", objects, truncated)
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

func TestManagementPatchKey(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	resp := env.do(t, http.MethodPatch, "/api/management/keys/"+env.memberUserID, env.adminToken, strings.NewReader(`{"display_name":"Renamed Member"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Key struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			IsActive    bool   `json:"is_active"`
		} `json:"key"`
	}
	decodeResponse(t, resp, &body)
	if body.Key.ID != env.memberUserID || body.Key.DisplayName != "Renamed Member" || !body.Key.IsActive {
		t.Fatalf("patched key = %+v", body.Key)
	}
}

func TestManagementPatchKeyDeactivatePreventsBearerAuthentication(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	resp := env.do(t, http.MethodPatch, "/api/management/keys/"+env.memberUserID, env.adminToken, strings.NewReader(`{"is_active":false}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", resp.StatusCode)
	}

	member := env.do(t, http.MethodGet, "/api/management/metrics", env.memberToken, nil)
	defer member.Body.Close()
	if member.StatusCode != http.StatusForbidden {
		t.Fatalf("inactive member status = %d, want 403", member.StatusCode)
	}
}

func TestManagementPatchKeyInvalidPayloads(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	cases := []string{
		`{}`,
		`{"display_name":"   "}`,
		`{"is_active":"no"}`,
		`{"unknown":true}`,
	}
	for _, payload := range cases {
		resp := env.do(t, http.MethodPatch, "/api/management/keys/"+env.memberUserID, env.adminToken, strings.NewReader(payload))
		if resp.StatusCode != http.StatusBadRequest {
			resp.Body.Close()
			t.Fatalf("payload %s status = %d, want 400", payload, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestManagementActivityAndConfig(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	activityRepo := metadata.NewActivityRepository(env.db)
	if err := activityRepo.Create(context.Background(), &metadata.ObjectActivity{
		ID:          "activity-1",
		Action:      "put_object",
		BucketName:  "photos",
		ObjectKey:   "a.jpg",
		Size:        10,
		ETag:        "etag-a",
		ActorUserID: env.adminUserID,
		CreatedAt:   time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("create activity: %v", err)
	}

	activityResp := env.do(t, http.MethodGet, "/api/management/activity?bucket=photos&action=put_object&limit=10", env.adminToken, nil)
	defer activityResp.Body.Close()
	if activityResp.StatusCode != http.StatusOK {
		t.Fatalf("activity status = %d, want 200", activityResp.StatusCode)
	}
	var activityBody struct {
		Activity []struct {
			ID          string `json:"id"`
			Action      string `json:"action"`
			Bucket      string `json:"bucket"`
			Key         string `json:"key"`
			ActorUserID string `json:"actor_user_id"`
		} `json:"activity"`
	}
	decodeResponse(t, activityResp, &activityBody)
	if len(activityBody.Activity) != 1 || activityBody.Activity[0].ID != "activity-1" {
		t.Fatalf("activity = %+v, want activity-1", activityBody.Activity)
	}

	configResp := env.do(t, http.MethodGet, "/api/management/config", env.adminToken, nil)
	defer configResp.Body.Close()
	if configResp.StatusCode != http.StatusOK {
		t.Fatalf("config status = %d, want 200", configResp.StatusCode)
	}
	configText := string(readBody(t, configResp))
	for _, forbidden := range []string{"db_path", "data_dir", "http_addr", "secret", "bind"} {
		if strings.Contains(configText, forbidden) {
			t.Fatalf("config response exposed %q: %s", forbidden, configText)
		}
	}
	if !strings.Contains(configText, `"region":"us-east-1"`) || !strings.Contains(configText, `"management_activity_limit":500`) {
		t.Fatalf("config response missing expected fields: %s", configText)
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

func TestManagementAuthAcceptsAdminSigV4(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	resp := env.doSigV4(t, http.MethodGet, "/api/management/metrics", env.adminSigV4, env.adminSigV4.SecretKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin SigV4 status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, readBody(t, resp))
	}
}

func TestManagementAuthRejectsMemberSigV4(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	resp := env.doSigV4(t, http.MethodGet, "/api/management/metrics", env.memberSigV4, env.memberSigV4.SecretKey)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member SigV4 status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestManagementAuthRejectsBadSigV4Signature(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	resp := env.doSigV4(t, http.MethodGet, "/api/management/metrics", env.adminSigV4, "wrong-secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad SigV4 status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
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

func TestManagementPublicURLSigningDisabled(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	resp := env.do(t, http.MethodPost, "/api/management/buckets/photos/objects/2026/image.jpg/public-url", env.adminToken, strings.NewReader(`{}`))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestManagementPublicURLSigningDisabledWhitespaceSecret(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.PublicReadSigningSecret = "   "
	env := newManagementTestEnvWithConfig(t, cfg)
	resp := env.do(t, http.MethodPost, "/api/management/buckets/photos/objects/2026/image.jpg/public-url", env.adminToken, strings.NewReader(`{}`))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestManagementPublicURLUsesPublicBaseURL(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.PublicBaseURL = "https://cdn.example.com"
	cfg.PublicReadSigningSecret = "12345678901234567890123456789012"
	env := newManagementTestEnvWithConfig(t, cfg)

	resp := env.do(t, http.MethodPost, "/api/management/buckets/photos/objects/2026/image.jpg/public-url", env.adminToken, strings.NewReader(`{"expires_in_seconds":3600}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, readBody(t, resp))
	}

	var body struct {
		URL          string `json:"url"`
		ExpiresAt    string `json:"expires_at"`
		CacheControl string `json:"cache_control"`
	}
	decodeResponse(t, resp, &body)
	if !strings.HasPrefix(body.URL, "https://cdn.example.com/public/photos/2026/image.jpg?") {
		t.Fatalf("url = %q, want public base URL", body.URL)
	}
	if body.ExpiresAt == "" {
		t.Fatal("expected expires_at")
	}
	if body.CacheControl != "public, max-age=3600, must-revalidate" {
		t.Fatalf("cache_control = %q, want max-age=3600", body.CacheControl)
	}
}

func TestManagementPublicURLOmittedTTLUsesDefault(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.PublicReadSigningSecret = "12345678901234567890123456789012"
	cfg.PublicReadDefaultTTL = 2 * time.Hour
	cfg.PublicReadMaxTTL = 3 * time.Hour
	env := newManagementTestEnvWithConfig(t, cfg)

	resp := env.do(t, http.MethodPost, "/api/management/buckets/photos/objects/2026/image.jpg/public-url", env.adminToken, strings.NewReader(`{}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, readBody(t, resp))
	}

	var body struct {
		CacheControl string `json:"cache_control"`
	}
	decodeResponse(t, resp, &body)
	if body.CacheControl != "public, max-age=7200, must-revalidate" {
		t.Fatalf("cache_control = %q, want default TTL", body.CacheControl)
	}
}

func TestManagementPublicURLRejectsTTLGreaterThanMax(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.PublicReadSigningSecret = "12345678901234567890123456789012"
	cfg.PublicReadMaxTTL = time.Hour
	env := newManagementTestEnvWithConfig(t, cfg)

	resp := env.do(t, http.MethodPost, "/api/management/buckets/photos/objects/2026/image.jpg/public-url", env.adminToken, strings.NewReader(`{"expires_in_seconds":3601}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestManagementPublicURLRejectsTTLExceedingMax(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.PublicReadSigningSecret = "12345678901234567890123456789012"
	cfg.PublicReadMaxTTL = 24 * time.Hour
	env := newManagementTestEnvWithConfig(t, cfg)

	resp := env.do(t, http.MethodPost, "/api/management/buckets/photos/objects/2026/image.jpg/public-url", env.adminToken, strings.NewReader(`{"expires_in_seconds":9223372036854775807}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestManagementPublicURLWorksAgainstPublicRoute(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.PublicReadSigningSecret = "12345678901234567890123456789012"
	env := newManagementTestEnvWithConfig(t, cfg)
	seedStoredObject(t, env, "photos", "cdn/file.txt", "cdn body")

	resp := env.do(t, http.MethodPost, "/api/management/buckets/photos/objects/cdn/file.txt/public-url", env.adminToken, strings.NewReader(`{"expires_in_seconds":3600}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, readBody(t, resp))
	}

	var body struct {
		URL string `json:"url"`
	}
	decodeResponse(t, resp, &body)

	publicURL, err := url.Parse(body.URL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	publicResp := env.do(t, http.MethodGet, publicURL.RequestURI(), "", nil)
	defer publicResp.Body.Close()
	if publicResp.StatusCode != http.StatusOK {
		t.Fatalf("public status = %d, want 200; body=%s", publicResp.StatusCode, readBody(t, publicResp))
	}
	if string(readBody(t, publicResp)) != "cdn body" {
		t.Fatalf("public body mismatch")
	}
}

func newManagementTestEnv(t *testing.T) managementTestEnv {
	t.Helper()
	return newManagementTestEnvWithConfig(t, config.Default())
}

func newManagementTestEnvWithConfig(t *testing.T, cfg config.Config) managementTestEnv {
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
	adminToken, adminSigV4, adminUser, err := auth.CreateBearerToken(ctx, userRepo, "Admin User", "admin")
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
	disk, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	var signer *publicread.Signer
	if strings.TrimSpace(cfg.PublicReadSigningSecret) != "" {
		signer, err = publicread.NewSigner(cfg.PublicReadSigningSecret, nil)
		if err != nil {
			t.Fatalf("new public read signer: %v", err)
		}
	}
	grantRepo := metadata.NewGrantRepository(db)
	handlers := &management.Handlers{
		Management:       metadata.NewManagementRepository(db),
		Buckets:          bucketRepo,
		Objects:          objectRepo,
		Activity:         metadata.NewActivityRepository(db),
		Users:            userRepo,
		Grants:           grantRepo,
		Storage:          disk,
		Config:           cfg,
		PublicReadSigner: signer,
	}
	authChain := &auth.ChainAuthenticator{
		Authenticators: []auth.Authenticator{
			&auth.BearerAuthenticator{Repo: userRepo},
			&auth.SigV4Authenticator{Repo: metadata.NewSigV4UserRepository(db)},
		},
	}

	routerCfg := cfg
	routerCfg.CORSAllowedOrigins = []string{"https://dashboard.example.com"}
	objectHandlers := &s3.ObjectHandlers{
		Users:            userRepo,
		Buckets:          bucketRepo,
		Objects:          objectRepo,
		Grants:           grantRepo,
		Authz:            s3.NewAuthzEvaluator(grantRepo),
		Storage:          disk,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		S3CacheControl:   cfg.S3CacheControl,
		PublicReadSigner: signer,
	}
	router := httpapi.NewRouter(routerCfg, nil, func(r chi.Router) {
		s3.RegisterPublicReadRoutes(r, objectHandlers)
		r.Route("/api/management", func(managementRoutes chi.Router) {
			managementRoutes.Use(auth.RequireAuthentication(authChain, management.WriteAuthError))
			management.RegisterGrantRoutes(managementRoutes, handlers)
			managementRoutes.Group(func(adminRoutes chi.Router) {
				adminRoutes.Use(auth.RequireRole("admin", management.WriteAuthError))
				management.RegisterAdminRoutes(adminRoutes, handlers)
			})
		})
	})

	return managementTestEnv{
		router:       router,
		db:           db,
		adminToken:   adminToken.RawToken,
		adminSigV4:   adminSigV4,
		adminUserID:  adminUser.ID,
		memberToken:  memberToken.RawToken,
		memberUserID: memberUser.ID,
		memberSigV4:  memberSigV4,
		storage:      disk,
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
		{ID: uuid.New().String(), BucketName: "photos", Key: "2026/image.jpg", Size: 10, ETag: "etag-image", ContentType: "image/jpeg", StoragePath: "hidden/image", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New().String(), BucketName: "photos", Key: "2026/raw/a.nef", Size: 20, ETag: "etag-raw-a", ContentType: "image/x-nikon-nef", StoragePath: "hidden/raw-a", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New().String(), BucketName: "photos", Key: "2026/raw/b.nef", Size: 30, ETag: "etag-raw-b", ContentType: "image/x-nikon-nef", StoragePath: "hidden/raw-b", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New().String(), BucketName: "docs", Key: "readme.txt", Size: 40, ETag: "etag-readme", ContentType: "text/plain", StoragePath: "hidden/readme", CreatedAt: now, UpdatedAt: now},
	}
	for _, obj := range objects {
		if err := objectRepo.Create(ctx, obj); err != nil {
			t.Fatalf("create object: %v", err)
		}
	}
}

func seedStoredObject(t *testing.T, env managementTestEnv, bucketName, key, body string) {
	t.Helper()

	storagePath, size, err := env.storage.Write(context.Background(), bucketName, key, strings.NewReader(body))
	if err != nil {
		t.Fatalf("write stored object: %v", err)
	}

	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	object := &metadata.Object{
		ID:          uuid.New().String(),
		BucketName:  bucketName,
		Key:         key,
		Size:        size,
		ETag:        "etag-cdn",
		ContentType: "text/plain",
		StoragePath: storagePath,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := metadata.NewObjectRepository(env.db).Create(context.Background(), object); err != nil {
		t.Fatalf("create stored object metadata: %v", err)
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

func (e managementTestEnv) doSigV4(t *testing.T, method, path string, credentials auth.SigV4Credentials, secretKey string) *http.Response {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(""))
	req.Host = "127.0.0.1:9000"
	auth.SignRequest(req, credentials.AccessKeyID, secretKey, "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"}, auth.EmptyStringHash)

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

func errorsIsBucketNotFound(err error) bool {
	return err == metadata.ErrBucketNotFound
}

func errorsIsObjectNotFound(err error) bool {
	return err == metadata.ErrObjectNotFound
}
