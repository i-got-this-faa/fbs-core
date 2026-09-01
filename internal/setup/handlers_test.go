package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/config"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func newSetupTestEnv(t *testing.T) (*chi.Mux, metadata.BootstrapRepository) {
	t.Helper()

	db, err := metadata.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
	})

	repo := metadata.NewBootstrapRepository(db)
	router := chi.NewRouter()
	RegisterRoutes(router, &Handlers{
		Bootstrap: repo,
		Config:    config.Default(),
	})

	return router, repo
}

func TestStatusEmptyDBRequiresBootstrap(t *testing.T) {
	router, _ := newSetupTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "127.0.0.1:9000"
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var body statusResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !body.BootstrapRequired {
		t.Fatal("bootstrap_required = false, want true")
	}
	if body.Region != "us-east-1" {
		t.Fatalf("region = %q, want us-east-1", body.Region)
	}
	if body.ManagementURL != "http://127.0.0.1:9000/api/management" {
		t.Fatalf("management_url = %q", body.ManagementURL)
	}
	if body.S3URL != "http://127.0.0.1:9000" {
		t.Fatalf("s3_url = %q", body.S3URL)
	}
}

func TestBootstrapCreatesInitialAdminAndReturnsCredentials(t *testing.T) {
	router, repo := newSetupTestEnv(t)

	body := postBootstrap(t, router, `{"display_name":"Admin User"}`, "127.0.0.1:12345")

	if body.code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", body.code, body.raw)
	}
	if body.cacheControl != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", body.cacheControl)
	}
	if strings.Contains(body.raw, "secret_hash") {
		t.Fatal("bootstrap response exposed secret_hash")
	}

	var resp bootstrapResponse
	if err := json.Unmarshal([]byte(body.raw), &resp); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if resp.Key.DisplayName != "Admin User" {
		t.Fatalf("display_name = %q, want Admin User", resp.Key.DisplayName)
	}
	if resp.Key.Role != "admin" {
		t.Fatalf("role = %q, want admin", resp.Key.Role)
	}
	if !resp.Key.IsActive {
		t.Fatal("is_active = false, want true")
	}
	if !strings.HasPrefix(resp.BearerToken, resp.Key.AccessKeyID+".") {
		t.Fatalf("bearer_token does not match access_key_id")
	}
	if resp.SigV4.AccessKeyID != resp.Key.SigV4AccessKeyID {
		t.Fatalf("sigv4 access key mismatch")
	}
	if resp.SigV4.SecretKey == "" {
		t.Fatal("sigv4 secret_key is empty")
	}

	count, err := repo.UserCount(context.Background())
	if err != nil {
		t.Fatalf("user count: %v", err)
	}
	if count != 1 {
		t.Fatalf("user count = %d, want 1", count)
	}
}

func TestSecondBootstrapAttemptReturnsConflict(t *testing.T) {
	router, _ := newSetupTestEnv(t)

	first := postBootstrap(t, router, `{}`, "127.0.0.1:12345")
	if first.code != http.StatusCreated {
		t.Fatalf("first status = %d, want 201; body=%s", first.code, first.raw)
	}

	second := postBootstrap(t, router, `{}`, "127.0.0.1:12346")
	if second.code != http.StatusConflict {
		t.Fatalf("second status = %d, want 409; body=%s", second.code, second.raw)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	req.RemoteAddr = "127.0.0.1:12347"
	req.Host = "127.0.0.1:9000"
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status after bootstrap = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var status statusResponse
	if err := json.NewDecoder(rr.Body).Decode(&status); err != nil {
		t.Fatalf("decode status after bootstrap: %v", err)
	}
	if status.BootstrapRequired {
		t.Fatal("bootstrap_required = true after first user exists, want false")
	}
}

func TestBootstrapRejectsNonLoopback(t *testing.T) {
	router, repo := newSetupTestEnv(t)

	resp := postBootstrap(t, router, `{}`, "203.0.113.10:12345")
	if resp.code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", resp.code, resp.raw)
	}

	count, err := repo.UserCount(context.Background())
	if err != nil {
		t.Fatalf("user count: %v", err)
	}
	if count != 0 {
		t.Fatalf("user count = %d, want 0", count)
	}
}

func TestConcurrentBootstrapCreatesOneAdmin(t *testing.T) {
	router, repo := newSetupTestEnv(t)
	server := httptest.NewTestServer(t, router)
	server.Start()
	client := server.Client()

	const requests = 12
	var wg sync.WaitGroup
	statusCodes := make(chan int, requests)
	for range requests {
		wg.Go(func() {
			resp, err := client.Post(server.URL+"/api/setup/bootstrap", "application/json", bytes.NewBufferString(`{}`))
			if err != nil {
				t.Errorf("post bootstrap: %v", err)
				return
			}
			defer resp.Body.Close()
			statusCodes <- resp.StatusCode
		})
	}
	wg.Wait()
	close(statusCodes)

	successes := 0
	conflicts := 0
	for code := range statusCodes {
		switch code {
		case http.StatusCreated:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected status code %d", code)
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want 1", successes)
	}
	if conflicts != requests-1 {
		t.Fatalf("conflicts = %d, want %d", conflicts, requests-1)
	}

	count, err := repo.UserCount(context.Background())
	if err != nil {
		t.Fatalf("user count: %v", err)
	}
	if count != 1 {
		t.Fatalf("user count = %d, want 1", count)
	}
}

type bootstrapTestResponse struct {
	code         int
	cacheControl string
	raw          string
}

func postBootstrap(t *testing.T, router http.Handler, requestBody string, remoteAddr string) bootstrapTestResponse {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/setup/bootstrap", strings.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	req.Host = "127.0.0.1:9000"
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	return bootstrapTestResponse{
		code:         rr.Code,
		cacheControl: rr.Header().Get("Cache-Control"),
		raw:          rr.Body.String(),
	}
}
