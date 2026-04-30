package auth

import "testing"

func TestValidateDevMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateDevMode(tt.addr, tt.devMode)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDevAuthenticator(t *testing.T) {
	t.Parallel()

	dev := &DevAuthenticator{}
	p, err := dev.Authenticate(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.UserID != "dev-user" {
		t.Errorf("UserID = %q, want dev-user", p.UserID)
	}
	if p.DisplayName != "Development User" {
		t.Errorf("DisplayName = %q, want Development User", p.DisplayName)
	}
	if p.AccessKeyID != "dev" {
		t.Errorf("AccessKeyID = %q, want dev", p.AccessKeyID)
	}
	if p.Role != "admin" {
		t.Errorf("Role = %q, want admin", p.Role)
	}
	if !p.DevMode {
		t.Error("expected DevMode to be true")
	}
}
