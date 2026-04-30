package auth

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

func ValidateDevMode(httpAddr string, devMode bool) error {
	if !devMode {
		return nil
	}

	host, _, err := net.SplitHostPort(httpAddr)
	if err != nil {
		return fmt.Errorf("parse http addr: %w", err)
	}

	switch strings.Trim(host, "[]") {
	case "127.0.0.1", "localhost", "::1":
		return nil
	default:
		return fmt.Errorf("--dev requires a localhost-only bind address")
	}
}

type DevAuthenticator struct{}

func (d *DevAuthenticator) Authenticate(_ *http.Request) (Principal, error) {
	return Principal{
		UserID:      "dev-user",
		DisplayName: "Development User",
		AccessKeyID: "dev",
		Role:        "admin",
		DevMode:     true,
	}, nil
}
