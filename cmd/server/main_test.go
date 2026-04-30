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

	binPath := buildServerBinary(t)
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

	user := &metadata.User{
		ID:          "user-test",
		DisplayName: "Test User",
		AccessKeyID: issued.AccessKeyID,
		SecretHash:  issued.SecretHash,
		Role:        "member",
		IsActive:    true,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
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
