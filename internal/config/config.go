package config

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultHTTPAddr        = "127.0.0.1:9000"
	defaultDBPath          = "./fbs.db"
	defaultReadTimeout     = 15 * time.Second
	defaultWriteTimeout    = 30 * time.Second
	defaultIdleTimeout     = 60 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

var defaultCORSAllowedOrigins = []string{
	"http://localhost:3000",
	"http://127.0.0.1:3000",
	"http://localhost:5173",
	"http://127.0.0.1:5173",
}

type Config struct {
	HTTPAddr           string
	DBPath             string
	PublicBaseURL      string
	CORSAllowedOrigins []string
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	IdleTimeout        time.Duration
	ShutdownTimeout    time.Duration
}

func Default() Config {
	return Config{
		HTTPAddr:           defaultHTTPAddr,
		DBPath:             defaultDBPath,
		CORSAllowedOrigins: append([]string(nil), defaultCORSAllowedOrigins...),
		ReadTimeout:        defaultReadTimeout,
		WriteTimeout:       defaultWriteTimeout,
		IdleTimeout:        defaultIdleTimeout,
		ShutdownTimeout:    defaultShutdownTimeout,
	}
}

func Load() (Config, error) {
	defaults := Default()

	readTimeout, err := durationFromEnv("FBS_READ_TIMEOUT", defaults.ReadTimeout)
	if err != nil {
		return Config{}, err
	}

	writeTimeout, err := durationFromEnv("FBS_WRITE_TIMEOUT", defaults.WriteTimeout)
	if err != nil {
		return Config{}, err
	}

	idleTimeout, err := durationFromEnv("FBS_IDLE_TIMEOUT", defaults.IdleTimeout)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := durationFromEnv("FBS_SHUTDOWN_TIMEOUT", defaults.ShutdownTimeout)
	if err != nil {
		return Config{}, err
	}

	flagSet := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	httpAddr := flagSet.String("http-addr", envOrDefault("FBS_HTTP_ADDR", defaults.HTTPAddr), "HTTP listen address")
	dbPath := flagSet.String("db-path", envOrDefault("FBS_DB_PATH", defaults.DBPath), "SQLite database path")
	publicBaseURL := flagSet.String("public-base-url", envOrDefault("FBS_PUBLIC_BASE_URL", defaults.PublicBaseURL), "Public base URL for ingress deployments")
	corsAllowedOrigins := flagSet.String(
		"cors-allowed-origins",
		envOrDefault("FBS_CORS_ALLOWED_ORIGINS", strings.Join(defaults.CORSAllowedOrigins, ",")),
		"Comma-separated list of allowed CORS origins",
	)
	flagSet.DurationVar(&readTimeout, "read-timeout", readTimeout, "HTTP read timeout")
	flagSet.DurationVar(&writeTimeout, "write-timeout", writeTimeout, "HTTP write timeout")
	flagSet.DurationVar(&idleTimeout, "idle-timeout", idleTimeout, "HTTP idle timeout")
	flagSet.DurationVar(&shutdownTimeout, "shutdown-timeout", shutdownTimeout, "HTTP shutdown timeout")

	if err := flagSet.Parse(os.Args[1:]); err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:           strings.TrimSpace(*httpAddr),
		DBPath:             strings.TrimSpace(*dbPath),
		PublicBaseURL:      strings.TrimSpace(*publicBaseURL),
		CORSAllowedOrigins: splitCSV(*corsAllowedOrigins),
		ReadTimeout:        readTimeout,
		WriteTimeout:       writeTimeout,
		IdleTimeout:        idleTimeout,
		ShutdownTimeout:    shutdownTimeout,
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.HTTPAddr == "" {
		return fmt.Errorf("http address is required")
	}

	if c.DBPath == "" {
		return fmt.Errorf("database path is required")
	}

	if c.PublicBaseURL != "" {
		if _, err := url.ParseRequestURI(c.PublicBaseURL); err != nil {
			return fmt.Errorf("invalid public base URL: %w", err)
		}
	}

	if len(c.CORSAllowedOrigins) == 0 {
		return fmt.Errorf("at least one CORS allowed origin is required")
	}

	if c.ReadTimeout <= 0 || c.WriteTimeout <= 0 || c.IdleTimeout <= 0 || c.ShutdownTimeout <= 0 {
		return fmt.Errorf("timeouts must be greater than zero")
	}

	return nil
}

func envOrDefault(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}

	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}

	return trimmed
}

func durationFromEnv(key string, fallback time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}

	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}

	return duration, nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	cleaned := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		cleaned = append(cleaned, trimmed)
	}

	return cleaned
}
