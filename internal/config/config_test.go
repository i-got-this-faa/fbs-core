package config

import (
	"os"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := Default()

	if cfg.HTTPAddr != defaultHTTPAddr {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, defaultHTTPAddr)
	}
	if cfg.DBPath != defaultDBPath {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, defaultDBPath)
	}
	if cfg.DataDir != defaultDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, defaultDataDir)
	}
	if cfg.DevMode {
		t.Error("expected DevMode to be false by default")
	}
}

func TestConfigValidate_DevModeRequiresLoopback(t *testing.T) {
	cases := []struct {
		name    string
		addr    string
		devMode bool
		wantErr bool
	}{
		{"dev off any addr", "0.0.0.0:9000", false, false},
		{"dev on loopback 127.0.0.1", "127.0.0.1:9000", true, false},
		{"dev on loopback localhost", "localhost:9000", true, false},
		{"dev on loopback ::1", "[::1]:9000", true, false},
		{"dev on public addr", "0.0.0.0:9000", true, true},
		{"dev on empty host", ":9000", true, true},
		{"dev on interface", "192.168.1.1:9000", true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.HTTPAddr = tc.addr
			cfg.DevMode = tc.devMode

			err := cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBoolFromEnvStrict(t *testing.T) {
	cases := []struct {
		name     string
		envValue string
		fallback bool
		want     bool
		wantErr  bool
	}{
		{"unset", "", false, false, false},
		{"empty", "", true, true, false},
		{"true lower", "true", false, true, false},
		{"true upper", "TRUE", false, true, false},
		{"1", "1", false, true, false},
		{"false lower", "false", true, false, false},
		{"false upper", "FALSE", true, false, false},
		{"0", "0", true, false, false},
		{"invalid", "maybe", false, false, true},
		{"invalid mixed", "YeS", false, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envValue != "" || tc.name == "empty" {
				t.Setenv("TEST_BOOL_STRICT", tc.envValue)
			} else {
				os.Unsetenv("TEST_BOOL_STRICT")
			}

			got, err := boolFromEnvStrict("TEST_BOOL_STRICT", tc.fallback)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBoolFromEnvFallbackOnInvalid(t *testing.T) {
	t.Setenv("TEST_BOOL_FALLBACK", "invalid")

	got := boolFromEnv("TEST_BOOL_FALLBACK", true)
	if !got {
		t.Error("expected fallback true on invalid parse")
	}
}

func TestLoad_DevFlag(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"fbs", "--dev", "--http-addr", "127.0.0.1:9001"}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.DevMode {
		t.Error("expected DevMode to be true")
	}
	if cfg.HTTPAddr != "127.0.0.1:9001" {
		t.Errorf("HTTPAddr = %q, want 127.0.0.1:9001", cfg.HTTPAddr)
	}
}

func TestLoad_DevFlagRejectsNonLoopback(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"fbs", "--dev", "--http-addr", "0.0.0.0:9000"}

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for --dev with non-loopback address")
	}
}

func TestLoad_DevEnvVar(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	t.Setenv("FBS_DEV", "true")
	os.Args = []string{"fbs", "--http-addr", "127.0.0.1:9000"}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.DevMode {
		t.Error("expected DevMode to be true from FBS_DEV")
	}
}

func TestLoad_DevEnvVarInvalid(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	t.Setenv("FBS_DEV", "maybe")
	os.Args = []string{"fbs"}

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid FBS_DEV value")
	}
}

func TestLoad_FlagOverridesEnv(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	t.Setenv("FBS_DEV", "false")
	os.Args = []string{"fbs", "--dev", "--http-addr", "127.0.0.1:9000"}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.DevMode {
		t.Error("expected --dev flag to override FBS_DEV=false")
	}
}

func TestConfigValidation_TimeoutsMustBePositive(t *testing.T) {
	cfg := Default()
	cfg.ReadTimeout = 0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for zero read timeout")
	}
}

func TestConfigValidation_EmptyHTTPAddr(t *testing.T) {
	cfg := Default()
	cfg.HTTPAddr = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty HTTPAddr")
	}
}

func TestConfigValidation_EmptyDBPath(t *testing.T) {
	cfg := Default()
	cfg.DBPath = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty DBPath")
	}
}

func TestConfigValidation_EmptyDataDir(t *testing.T) {
	cfg := Default()
	cfg.DataDir = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty DataDir")
	}
}

func TestConfigValidation_InvalidPublicBaseURL(t *testing.T) {
	cfg := Default()
	cfg.PublicBaseURL = "://invalid"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid PublicBaseURL")
	}
}

func TestConfigValidation_EmptyCORSOrigins(t *testing.T) {
	cfg := Default()
	cfg.CORSAllowedOrigins = []string{}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty CORSAllowedOrigins")
	}
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}},
		{"a,,b", []string{"a", "b"}},
		{"", []string{}},
		{",,", []string{}},
	}

	for _, tc := range cases {
		got := splitCSV(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func TestDurationFromEnv(t *testing.T) {
	t.Setenv("TEST_DURATION", "30s")
	got, err := durationFromEnv("TEST_DURATION", 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 30*time.Second {
		t.Errorf("got %v, want 30s", got)
	}

	got, err = durationFromEnv("TEST_DURATION_UNSET", 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 10*time.Second {
		t.Errorf("got %v, want 10s", got)
	}

	t.Setenv("TEST_DURATION_INVALID", "not-a-duration")
	_, err = durationFromEnv("TEST_DURATION_INVALID", 10*time.Second)
	if err == nil {
		t.Error("expected error for invalid duration")
	}
}

func TestEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_ENV", "value")
	if got := envOrDefault("TEST_ENV", "fallback"); got != "value" {
		t.Errorf("got %q, want value", got)
	}

	os.Unsetenv("TEST_ENV_UNSET")
	if got := envOrDefault("TEST_ENV_UNSET", "fallback"); got != "fallback" {
		t.Errorf("got %q, want fallback", got)
	}

	t.Setenv("TEST_ENV_EMPTY", "   ")
	if got := envOrDefault("TEST_ENV_EMPTY", "fallback"); got != "fallback" {
		t.Errorf("got %q, want fallback for whitespace-only", got)
	}
}

func TestFlagSetErrorPropagates(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"fbs", "--invalid-flag"}
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid flag")
	}
}
