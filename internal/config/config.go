package config

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/i-got-this-faa/fbs/internal/auth"
)

const (
	defaultHTTPAddr         = "127.0.0.1:9000"
	defaultDBPath           = "./fbs.db"
	defaultDataDir          = "./data"
	defaultMetadataCache    = int64(512 * 1024 * 1024)
	defaultS3CacheControl   = "private, max-age=0, must-revalidate"
	defaultPublicReadTTL    = time.Hour
	defaultPublicReadMaxTTL = 24 * time.Hour
	defaultReadTimeout      = 15 * time.Second
	defaultWriteTimeout     = 30 * time.Second
	defaultIdleTimeout      = 60 * time.Second
	defaultShutdownTimeout  = 10 * time.Second
)

var defaultCORSAllowedOrigins = []string{
	"http://localhost:3000",
	"http://127.0.0.1:3000",
	"http://localhost:5173",
	"http://127.0.0.1:5173",
}

type Config struct {
	HTTPAddr                string
	DBPath                  string
	DataDir                 string
	DevMode                 bool
	PublicBaseURL           string
	MetadataCacheSizeBytes  int64
	S3CacheControl          string
	PublicReadSigningSecret string
	PublicReadDefaultTTL    time.Duration
	PublicReadMaxTTL        time.Duration
	CORSAllowedOrigins      []string
	ReadTimeout             time.Duration
	WriteTimeout            time.Duration
	IdleTimeout             time.Duration
	ShutdownTimeout         time.Duration
}

func Default() Config {
	return Config{
		HTTPAddr:               defaultHTTPAddr,
		DBPath:                 defaultDBPath,
		DataDir:                defaultDataDir,
		MetadataCacheSizeBytes: defaultMetadataCache,
		S3CacheControl:         defaultS3CacheControl,
		PublicReadDefaultTTL:   defaultPublicReadTTL,
		PublicReadMaxTTL:       defaultPublicReadMaxTTL,
		CORSAllowedOrigins:     append([]string(nil), defaultCORSAllowedOrigins...),
		ReadTimeout:            defaultReadTimeout,
		WriteTimeout:           defaultWriteTimeout,
		IdleTimeout:            defaultIdleTimeout,
		ShutdownTimeout:        defaultShutdownTimeout,
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

	metadataCacheSize, err := byteSizeFromEnv("FBS_METADATA_CACHE_SIZE", defaults.MetadataCacheSizeBytes)
	if err != nil {
		return Config{}, err
	}

	publicReadDefaultTTL, err := durationFromEnv("FBS_PUBLIC_READ_DEFAULT_TTL", defaults.PublicReadDefaultTTL)
	if err != nil {
		return Config{}, err
	}

	publicReadMaxTTL, err := durationFromEnv("FBS_PUBLIC_READ_MAX_TTL", defaults.PublicReadMaxTTL)
	if err != nil {
		return Config{}, err
	}

	flagSet := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	httpAddr := flagSet.String("http-addr", envOrDefault("FBS_HTTP_ADDR", defaults.HTTPAddr), "HTTP listen address")
	dbPath := flagSet.String("db-path", envOrDefault("FBS_DB_PATH", defaults.DBPath), "SQLite database path")
	dataDir := flagSet.String("data-dir", envOrDefault("FBS_DATA_DIR", defaults.DataDir), "Data root directory")
	devMode, err := boolFromEnvStrict("FBS_DEV", defaults.DevMode)
	if err != nil {
		return Config{}, err
	}
	devModeFlag := flagSet.Bool("dev", devMode, "Development mode (bypasses authentication)")
	publicBaseURL := flagSet.String("public-base-url", envOrDefault("FBS_PUBLIC_BASE_URL", defaults.PublicBaseURL), "Public base URL for ingress deployments")
	metadataCacheSizeFlag := flagSet.String("metadata-cache-size", strconv.FormatInt(metadataCacheSize, 10), "Metadata cache byte budget")
	s3CacheControl := flagSet.String("s3-cache-control", envRawOrDefault("FBS_S3_CACHE_CONTROL", defaults.S3CacheControl), "Cache-Control for authenticated S3 object reads")
	publicReadSigningSecret := flagSet.String("public-read-signing-secret", envRawOrDefault("FBS_PUBLIC_READ_SIGNING_SECRET", defaults.PublicReadSigningSecret), "Secret for signed public read URLs")
	corsAllowedOrigins := flagSet.String(
		"cors-allowed-origins",
		envOrDefault("FBS_CORS_ALLOWED_ORIGINS", strings.Join(defaults.CORSAllowedOrigins, ",")),
		"Comma-separated list of allowed CORS origins",
	)
	flagSet.DurationVar(&publicReadDefaultTTL, "public-read-default-ttl", publicReadDefaultTTL, "Default TTL for signed public read URLs")
	flagSet.DurationVar(&publicReadMaxTTL, "public-read-max-ttl", publicReadMaxTTL, "Maximum TTL for signed public read URLs")
	flagSet.DurationVar(&readTimeout, "read-timeout", readTimeout, "HTTP read timeout")
	flagSet.DurationVar(&writeTimeout, "write-timeout", writeTimeout, "HTTP write timeout")
	flagSet.DurationVar(&idleTimeout, "idle-timeout", idleTimeout, "HTTP idle timeout")
	flagSet.DurationVar(&shutdownTimeout, "shutdown-timeout", shutdownTimeout, "HTTP shutdown timeout")

	if err := flagSet.Parse(os.Args[1:]); err != nil {
		return Config{}, err
	}

	parsedMetadataCacheSize, err := parseByteSize(*metadataCacheSizeFlag)
	if err != nil {
		return Config{}, fmt.Errorf("parse metadata cache size: %w", err)
	}

	cfg := Config{
		HTTPAddr:                strings.TrimSpace(*httpAddr),
		DBPath:                  strings.TrimSpace(*dbPath),
		DataDir:                 strings.TrimSpace(*dataDir),
		DevMode:                 *devModeFlag,
		PublicBaseURL:           strings.TrimSpace(*publicBaseURL),
		MetadataCacheSizeBytes:  parsedMetadataCacheSize,
		S3CacheControl:          strings.TrimSpace(*s3CacheControl),
		PublicReadSigningSecret: strings.TrimSpace(*publicReadSigningSecret),
		PublicReadDefaultTTL:    publicReadDefaultTTL,
		PublicReadMaxTTL:        publicReadMaxTTL,
		CORSAllowedOrigins:      splitCSV(*corsAllowedOrigins),
		ReadTimeout:             readTimeout,
		WriteTimeout:            writeTimeout,
		IdleTimeout:             idleTimeout,
		ShutdownTimeout:         shutdownTimeout,
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

	if c.DataDir == "" {
		return fmt.Errorf("data directory is required")
	}

	if c.MetadataCacheSizeBytes < 0 {
		return fmt.Errorf("metadata cache size must be greater than or equal to zero")
	}

	if c.PublicBaseURL != "" {
		if _, err := url.ParseRequestURI(c.PublicBaseURL); err != nil {
			return fmt.Errorf("invalid public base URL: %w", err)
		}
	}

	if strings.TrimSpace(c.PublicReadSigningSecret) != "" && len([]byte(strings.TrimSpace(c.PublicReadSigningSecret))) < 32 {
		return fmt.Errorf("public read signing secret must be at least 32 bytes")
	}

	if c.PublicReadDefaultTTL <= 0 || c.PublicReadMaxTTL <= 0 {
		return fmt.Errorf("public read TTLs must be greater than zero")
	}
	if c.PublicReadDefaultTTL > c.PublicReadMaxTTL {
		return fmt.Errorf("public read default TTL must not exceed max TTL")
	}

	if len(c.CORSAllowedOrigins) == 0 {
		return fmt.Errorf("at least one CORS allowed origin is required")
	}

	if c.ReadTimeout <= 0 || c.WriteTimeout <= 0 || c.IdleTimeout <= 0 || c.ShutdownTimeout <= 0 {
		return fmt.Errorf("timeouts must be greater than zero")
	}

	if c.DevMode {
		if err := auth.ValidateDevMode(c.HTTPAddr, c.DevMode); err != nil {
			return err
		}
	}

	return nil
}

func envRawOrDefault(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	return value
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

func byteSizeFromEnv(key string, fallback int64) (int64, error) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}

	size, err := parseByteSize(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}

	return size, nil
}

func parseByteSize(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("byte size is required")
	}

	units := map[string]int64{
		"KiB": 1024,
		"MiB": 1024 * 1024,
		"GiB": 1024 * 1024 * 1024,
		"KB":  1000,
		"MB":  1000 * 1000,
		"GB":  1000 * 1000 * 1000,
	}

	multiplier := int64(1)
	number := trimmed
	for suffix, unitMultiplier := range units {
		if strings.HasSuffix(trimmed, suffix) {
			multiplier = unitMultiplier
			number = strings.TrimSpace(strings.TrimSuffix(trimmed, suffix))
			break
		}
	}
	if number == "" {
		return 0, fmt.Errorf("missing byte size value")
	}

	size, err := strconv.ParseInt(number, 10, 64)
	if err != nil {
		return 0, err
	}
	if size < 0 {
		return 0, fmt.Errorf("byte size must be greater than or equal to zero")
	}
	if size > (1<<63-1)/multiplier {
		return 0, fmt.Errorf("byte size overflows int64")
	}

	return size * multiplier, nil
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

func boolFromEnvStrict(key string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}

	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback, nil
	}

	b, err := strconv.ParseBool(trimmed)
	if err != nil {
		return false, fmt.Errorf("invalid value for %s: %q", key, value)
	}

	return b, nil
}
