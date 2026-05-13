package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func buildServerBinary(t *testing.T) string {
	t.Helper()

	binPath := filepath.Join(t.TempDir(), "fbs-server")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Env = os.Environ()

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build server binary: %v\noutput:\n%s", err, string(out))
	}
	return binPath
}

func buildServerBinaryWithTestEndpoints(t *testing.T) string {
	t.Helper()

	binPath := filepath.Join(t.TempDir(), "fbs-server-testendpoints")
	cmd := exec.Command("go", "build", "-tags", "testendpoints", "-o", binPath, ".")
	cmd.Env = os.Environ()

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build server binary with testendpoints tag: %v\noutput:\n%s", err, string(out))
	}
	return binPath
}

func TestBuildServerBinary(t *testing.T) {
	buildServerBinary(t)
}

func TestServerGracefulShutdown(t *testing.T) {
	binPath := buildServerBinary(t)

	cmd := exec.Command(binPath)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"HTTP_ADDR=127.0.0.1:0",
		"FBS_HTTP_ADDR=127.0.0.1:0",
		"SHUTDOWN_TIMEOUT=1s",
		"FBS_SHUTDOWN_TIMEOUT=1s",
	)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	started := make(chan struct{})

	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			stdoutBuf.WriteString(line)
			stdoutBuf.WriteByte('\n')
			if strings.Contains(line, "starting server") {
				select {
				case <-started:
				default:
					close(started)
				}
			}
		}
	}()

	go func() {
		_, _ = io.Copy(&stderrBuf, stderrPipe)
	}()

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("server did not start in time; stdout=%q stderr=%q", stdoutBuf.String(), stderrBuf.String())
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server exited with error: %v\nstdout=%q\nstderr=%q", err, stdoutBuf.String(), stderrBuf.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("server did not exit after SIGTERM; stdout=%q stderr=%q", stdoutBuf.String(), stderrBuf.String())
	}

	if !strings.Contains(stdoutBuf.String(), "shutting down server") {
		t.Fatalf("expected shutdown log; stdout=%q stderr=%q", stdoutBuf.String(), stderrBuf.String())
	}
}

func findFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func startTestServer(t *testing.T, extraEnv ...string) (cmd *exec.Cmd, baseURL string, shutdown func()) {
	t.Helper()

	binPath := buildServerBinaryWithTestEndpoints(t)
	workDir := t.TempDir()
	addr := findFreePort(t)
	baseURL = "http://" + addr

	cmd = exec.Command(binPath)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"FBS_HTTP_ADDR="+addr,
		"FBS_SHUTDOWN_TIMEOUT=1s",
	)
	cmd.Env = append(cmd.Env, extraEnv...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	started := make(chan struct{})

	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			stdoutBuf.WriteString(line)
			stdoutBuf.WriteByte('\n')
			if strings.Contains(line, "starting server") {
				select {
				case <-started:
				default:
					close(started)
				}
			}
		}
	}()

	go func() {
		_, _ = io.Copy(&stderrBuf, stderrPipe)
	}()

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("server did not start in time; stdout=%q stderr=%q", stdoutBuf.String(), stderrBuf.String())
	}

	// Give the server a moment to actually bind
	time.Sleep(100 * time.Millisecond)

	shutdown = func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
	}

	return cmd, baseURL, shutdown
}

func TestServerAuth_DevModeBypass(t *testing.T) {
	_, baseURL, shutdown := startTestServer(t, "FBS_DEV=true")
	defer shutdown()

	resp, err := http.Get(baseURL + "/_health/auth")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if body["user_id"] != "dev-user" {
		t.Errorf("user_id = %q, want dev-user", body["user_id"])
	}
	if body["role"] != "admin" {
		t.Errorf("role = %q, want admin", body["role"])
	}
	if body["dev_mode"] != true {
		t.Errorf("dev_mode = %v, want true", body["dev_mode"])
	}
}

func TestServerAuth_ProtectedRouteRequiresAuth(t *testing.T) {
	_, baseURL, shutdown := startTestServer(t)
	defer shutdown()

	resp, err := http.Get(baseURL + "/_health/auth")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(wwwAuth, `Bearer realm="fbs"`) {
		t.Fatalf("expected WWW-Authenticate header, got %q", wwwAuth)
	}
}

func TestServerAuth_BearerToken(t *testing.T) {
	workDir := t.TempDir()
	dbPath := filepath.Join(workDir, "test.db")

	db, err := metadata.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	issued, err := auth.IssueBearerToken()
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	sigv4, err := auth.IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-test",
		DisplayName:      "Test User",
		AccessKeyID:      issued.AccessKeyID,
		SecretHash:       issued.SecretHash,
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := metadata.NewUserRepository(db).Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	db.Close()

	_, baseURL, shutdown := startTestServer(t, "FBS_DB_PATH="+dbPath)
	defer shutdown()

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/_health/auth", nil)
	req.Header.Set("Authorization", "Bearer "+issued.RawToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if body["user_id"] != "user-test" {
		t.Errorf("user_id = %q, want user-test", body["user_id"])
	}
	if body["role"] != "member" {
		t.Errorf("role = %q, want member", body["role"])
	}
	if body["dev_mode"] != false {
		t.Errorf("dev_mode = %v, want false", body["dev_mode"])
	}
}

func TestServerAuth_UnsupportedScheme(t *testing.T) {
	_, baseURL, shutdown := startTestServer(t)
	defer shutdown()

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/_health/auth", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	if strings.Contains(resp.Header.Get("WWW-Authenticate"), `Bearer realm="fbs"`) {
		t.Error("unsupported scheme should not trigger WWW-Authenticate")
	}
}

func TestServerAuth_SigV4(t *testing.T) {
	workDir := t.TempDir()
	dbPath := filepath.Join(workDir, "test.db")

	db, err := metadata.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	sigv4, err := auth.IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-sigv4",
		DisplayName:      "SigV4 Test User",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "admin",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := metadata.NewUserRepository(db).Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	db.Close()

	_, baseURL, shutdown := startTestServer(t, "FBS_DB_PATH="+dbPath)
	defer shutdown()

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/_health/auth", nil)
	req.Host = strings.TrimPrefix(baseURL, "http://")

	auth.SignRequest(req, sigv4.AccessKeyID, sigv4.SecretKey, "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"}, auth.EmptyStringHash)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if body["user_id"] != "user-sigv4" {
		t.Errorf("user_id = %q, want user-sigv4", body["user_id"])
	}
	if body["role"] != "admin" {
		t.Errorf("role = %q, want admin", body["role"])
	}
	if body["dev_mode"] != false {
		t.Errorf("dev_mode = %v, want false", body["dev_mode"])
	}
}

func TestServerAuth_SigV4WrongSignature(t *testing.T) {
	workDir := t.TempDir()
	dbPath := filepath.Join(workDir, "test.db")

	db, err := metadata.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	sigv4, err := auth.IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-sigv4-wrong",
		DisplayName:      "SigV4 Wrong",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := metadata.NewUserRepository(db).Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	db.Close()

	_, baseURL, shutdown := startTestServer(t, "FBS_DB_PATH="+dbPath)
	defer shutdown()

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/_health/auth", nil)
	req.Host = strings.TrimPrefix(baseURL, "http://")

	auth.SignRequest(req, sigv4.AccessKeyID, "wrong-secret", "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"}, auth.EmptyStringHash)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestServerSetupBootstrapCredentialsAccessManagement(t *testing.T) {
	workDir := t.TempDir()
	dbPath := filepath.Join(workDir, "test.db")

	_, baseURL, shutdown := startTestServer(t, "FBS_DB_PATH="+dbPath)
	defer shutdown()

	statusResp, err := http.Get(baseURL + "/api/setup/status")
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}
	defer statusResp.Body.Close()
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d, want 200", statusResp.StatusCode)
	}
	var statusBody struct {
		BootstrapRequired bool   `json:"bootstrap_required"`
		Region            string `json:"region"`
		ManagementURL     string `json:"management_url"`
		S3URL             string `json:"s3_url"`
	}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusBody); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !statusBody.BootstrapRequired {
		t.Fatal("bootstrap_required = false, want true")
	}
	if statusBody.Region != "us-east-1" {
		t.Fatalf("region = %q, want us-east-1", statusBody.Region)
	}

	bootstrapResp, err := http.Post(baseURL+"/api/setup/bootstrap", "application/json", strings.NewReader(`{"display_name":"Admin User"}`))
	if err != nil {
		t.Fatalf("bootstrap request failed: %v", err)
	}
	defer bootstrapResp.Body.Close()
	bootstrapBodyBytes, err := io.ReadAll(bootstrapResp.Body)
	if err != nil {
		t.Fatalf("read bootstrap response: %v", err)
	}
	if bootstrapResp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap code = %d, want 201; body=%s", bootstrapResp.StatusCode, string(bootstrapBodyBytes))
	}
	if bootstrapResp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", bootstrapResp.Header.Get("Cache-Control"))
	}
	if strings.Contains(string(bootstrapBodyBytes), "secret_hash") {
		t.Fatalf("bootstrap response exposed secret_hash: %s", string(bootstrapBodyBytes))
	}

	var bootstrapBody struct {
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
		Region        string `json:"region"`
		ManagementURL string `json:"management_url"`
		S3URL         string `json:"s3_url"`
	}
	if err := json.Unmarshal(bootstrapBodyBytes, &bootstrapBody); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if bootstrapBody.Key.Role != "admin" || !bootstrapBody.Key.IsActive {
		t.Fatalf("bootstrap key = %+v, want active admin", bootstrapBody.Key)
	}

	bearerReq, _ := http.NewRequest(http.MethodGet, baseURL+"/api/management/metrics", nil)
	bearerReq.Header.Set("Authorization", "Bearer "+bootstrapBody.BearerToken)
	bearerResp, err := http.DefaultClient.Do(bearerReq)
	if err != nil {
		t.Fatalf("bearer metrics request failed: %v", err)
	}
	defer bearerResp.Body.Close()
	if bearerResp.StatusCode != http.StatusOK {
		t.Fatalf("bearer metrics code = %d, want 200", bearerResp.StatusCode)
	}

	sigV4Req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/management/metrics", nil)
	sigV4Req.Host = strings.TrimPrefix(baseURL, "http://")
	auth.SignRequest(sigV4Req, bootstrapBody.SigV4.AccessKeyID, bootstrapBody.SigV4.SecretKey, "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"}, auth.EmptyStringHash)
	sigV4Resp, err := http.DefaultClient.Do(sigV4Req)
	if err != nil {
		t.Fatalf("sigv4 metrics request failed: %v", err)
	}
	defer sigV4Resp.Body.Close()
	if sigV4Resp.StatusCode != http.StatusOK {
		t.Fatalf("sigv4 metrics code = %d, want 200", sigV4Resp.StatusCode)
	}

	secondResp, err := http.Post(baseURL+"/api/setup/bootstrap", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("second bootstrap request failed: %v", err)
	}
	defer secondResp.Body.Close()
	if secondResp.StatusCode != http.StatusConflict {
		t.Fatalf("second bootstrap code = %d, want 409", secondResp.StatusCode)
	}
}

func TestServerStartsWithWhitespacePublicReadSigningSecret(t *testing.T) {
	_, _, shutdown := startTestServer(t, "FBS_PUBLIC_READ_SIGNING_SECRET=   ")
	defer shutdown()
}
