package main

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
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
