package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func TestSigV4AuthSuccess(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-sigv4",
		DisplayName:      "SigV4 User",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "admin",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo}
	req := httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	req.Host = "localhost:9000"

	SignRequest(req, sigv4.AccessKeyID, sigv4.SecretKey, "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"}, EmptyStringHash)

	p, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.UserID != user.ID {
		t.Errorf("UserID = %q, want %q", p.UserID, user.ID)
	}
	if p.Role != "admin" {
		t.Errorf("Role = %q, want admin", p.Role)
	}
	if p.DevMode {
		t.Error("expected DevMode to be false")
	}
}

func TestSigV4AuthMissingHeader(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	auth := &SigV4Authenticator{Repo: metadata.NewSigV4UserRepository(db)}
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	_, err := auth.Authenticate(req)
	if err != ErrNotApplicable {
		t.Fatalf("expected ErrNotApplicable, got %v", err)
	}
}

func TestSigV4AuthUnsupportedScheme(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	auth := &SigV4Authenticator{Repo: metadata.NewSigV4UserRepository(db)}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer token")

	_, err := auth.Authenticate(req)
	if err != ErrNotApplicable {
		t.Fatalf("expected ErrNotApplicable, got %v", err)
	}
}

func TestSigV4AuthMalformedCredential(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	auth := &SigV4Authenticator{Repo: metadata.NewSigV4UserRepository(db)}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=bad, SignedHeaders=host, Signature=abc")

	_, err := auth.Authenticate(req)
	if err != ErrMalformedToken {
		t.Fatalf("expected ErrMalformedToken, got %v", err)
	}
}

func TestSigV4AuthUnknownUser(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	auth := &SigV4Authenticator{Repo: metadata.NewSigV4UserRepository(db)}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "localhost:9000"

	SignRequest(req, "fbsv4_unknown", "secret", "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"}, EmptyStringHash)

	_, err := auth.Authenticate(req)
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestSigV4AuthInactiveUser(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-inactive",
		DisplayName:      "Inactive",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         false,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "localhost:9000"

	SignRequest(req, sigv4.AccessKeyID, sigv4.SecretKey, "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"}, EmptyStringHash)

	_, err = auth.Authenticate(req)
	if err != ErrInactiveUser {
		t.Fatalf("expected ErrInactiveUser, got %v", err)
	}
}

func TestSigV4AuthWrongSignature(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-wrong",
		DisplayName:      "Wrong Secret",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "localhost:9000"

	SignRequest(req, sigv4.AccessKeyID, "wrong-secret", "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"}, EmptyStringHash)

	_, err = auth.Authenticate(req)
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestSigV4AuthClockSkew(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-skew",
		DisplayName:      "Skew Test",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Request signed 20 minutes ago
	past := time.Now().UTC().Add(-20 * time.Minute)
	auth := &SigV4Authenticator{Repo: sigv4Repo, Now: time.Now().UTC}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "localhost:9000"

	SignRequestWithContext(context.Background(), req, sigv4.AccessKeyID, sigv4.SecretKey, "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"}, EmptyStringHash, past)

	_, err = auth.Authenticate(req)
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for clock skew, got %v", err)
	}
}

func TestSigV4AuthQueryString(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-query",
		DisplayName:      "Query User",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo}
	now := time.Now().UTC()
	timestamp := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	credential := sigv4.AccessKeyID + "/" + date + "/us-east-1/s3/aws4_request"
	signedHeaders := "host"

	// Build a presigned URL manually
	req := httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	req.Host = "localhost:9000"
	req.Header.Set("Host", req.Host)

	// Set query params before computing signature so canonical query string is correct
	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", sigV4Algorithm)
	q.Set("X-Amz-Credential", credential)
	q.Set("X-Amz-Date", timestamp)
	q.Set("X-Amz-Expires", "3600")
	q.Set("X-Amz-SignedHeaders", signedHeaders)
	req.URL.RawQuery = q.Encode()

	canonicalRequest := buildCanonicalRequestForQuery(req, signedHeaders, unsignedPayload)
	stringToSign := buildStringToSign(timestamp, credential, canonicalRequest)
	signature := computeSignature(sigv4.SecretKey, date, "us-east-1", "s3", stringToSign)

	q.Set("X-Amz-Signature", signature)
	req.URL.RawQuery = q.Encode()

	p, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.UserID != user.ID {
		t.Errorf("UserID = %q, want %q", p.UserID, user.ID)
	}
}

func TestSigV4AuthQueryStringExpired(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-expired",
		DisplayName:      "Expired",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo, Now: time.Now().UTC}
	past := time.Now().UTC().Add(-2 * time.Hour)
	timestamp := past.Format("20060102T150405Z")
	date := past.Format("20060102")
	credential := sigv4.AccessKeyID + "/" + date + "/us-east-1/s3/aws4_request"
	signedHeaders := "host"

	req := httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	req.Host = "localhost:9000"
	req.Header.Set("Host", req.Host)

	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", sigV4Algorithm)
	q.Set("X-Amz-Credential", credential)
	q.Set("X-Amz-Date", timestamp)
	q.Set("X-Amz-Expires", "60") // 60 seconds, but signed 2 hours ago
	q.Set("X-Amz-SignedHeaders", signedHeaders)
	req.URL.RawQuery = q.Encode()

	canonicalRequest := buildCanonicalRequestForQuery(req, signedHeaders, unsignedPayload)
	stringToSign := buildStringToSign(timestamp, credential, canonicalRequest)
	signature := computeSignature(sigv4.SecretKey, date, "us-east-1", "s3", stringToSign)

	q.Set("X-Amz-Signature", signature)
	req.URL.RawQuery = q.Encode()

	_, err = auth.Authenticate(req)
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for expired URL, got %v", err)
	}
}

func TestSigV4AuthInternalError(t *testing.T) {
	t.Parallel()

	auth := &SigV4Authenticator{Repo: &failingUserRepo{}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "localhost:9000"

	SignRequest(req, "fbsv4_test", "secret", "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"}, EmptyStringHash)

	_, err := auth.Authenticate(req)
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("expected ErrInternal, got %v", err)
	}
}

func TestSigV4ChainAuthenticator(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-chain",
		DisplayName:      "Chain User",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "admin",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	chain := &ChainAuthenticator{
		Authenticators: []Authenticator{
			&BearerAuthenticator{Repo: repo},
			&SigV4Authenticator{Repo: sigv4Repo},
		},
	}

	// Test that a SigV4 request succeeds through the chain
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "localhost:9000"
	SignRequest(req, sigv4.AccessKeyID, sigv4.SecretKey, "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"}, EmptyStringHash)

	p, err := chain.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.UserID != user.ID {
		t.Errorf("UserID = %q, want %q", p.UserID, user.ID)
	}
}

func TestSigV4ChainAuthenticator_BearerFirst(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	user, rawToken := createTestUser(t, repo, "BearerFirst", "member", true)

	chain := &ChainAuthenticator{
		Authenticators: []Authenticator{
			&BearerAuthenticator{Repo: repo},
			&SigV4Authenticator{Repo: sigv4Repo},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

	p, err := chain.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.UserID != user.ID {
		t.Errorf("UserID = %q, want %q", p.UserID, user.ID)
	}
}

func TestSigV4ChainAuthenticator_NoApplicableAuth(t *testing.T) {
	t.Parallel()

	chain := &ChainAuthenticator{
		Authenticators: []Authenticator{
			&BearerAuthenticator{},
			&SigV4Authenticator{},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := chain.Authenticate(req)
	if err != ErrMissingAuth {
		t.Fatalf("expected ErrMissingAuth, got %v", err)
	}
}

func TestSigV4ChainAuthenticator_UnsupportedScheme(t *testing.T) {
	t.Parallel()

	chain := &ChainAuthenticator{
		Authenticators: []Authenticator{
			&BearerAuthenticator{},
			&SigV4Authenticator{},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	_, err := chain.Authenticate(req)
	if err != ErrUnsupportedScheme {
		t.Fatalf("expected ErrUnsupportedScheme, got %v", err)
	}
}

func TestSigV4AuthQueryStringOlderThanClockSkewButWithinExpiry(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-old-query",
		DisplayName:      "Old Query",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo, Now: time.Now().UTC}
	past := time.Now().UTC().Add(-30 * time.Minute)
	timestamp := past.Format("20060102T150405Z")
	date := past.Format("20060102")
	credential := sigv4.AccessKeyID + "/" + date + "/us-east-1/s3/aws4_request"
	signedHeaders := "host"

	req := httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	req.Host = "localhost:9000"
	req.Header.Set("Host", req.Host)

	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", sigV4Algorithm)
	q.Set("X-Amz-Credential", credential)
	q.Set("X-Amz-Date", timestamp)
	q.Set("X-Amz-Expires", "3600") // 1 hour, signed 30 minutes ago
	q.Set("X-Amz-SignedHeaders", signedHeaders)
	req.URL.RawQuery = q.Encode()

	canonicalRequest := buildCanonicalRequestForQuery(req, signedHeaders, unsignedPayload)
	stringToSign := buildStringToSign(timestamp, credential, canonicalRequest)
	signature := computeSignature(sigv4.SecretKey, date, "us-east-1", "s3", stringToSign)

	q.Set("X-Amz-Signature", signature)
	req.URL.RawQuery = q.Encode()

	p, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.UserID != user.ID {
		t.Errorf("UserID = %q, want %q", p.UserID, user.ID)
	}
}

func TestSigV4AuthHeaderWithoutCommaSpace(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-nospace",
		DisplayName:      "No Space",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "admin",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo}
	req := httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	req.Host = "localhost:9000"

	// Sign normally, then rewrite the header to remove spaces after commas
	SignRequest(req, sigv4.AccessKeyID, sigv4.SecretKey, "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"}, EmptyStringHash)
	authHeader := req.Header.Get("Authorization")
	authHeader = strings.ReplaceAll(authHeader, ", ", ",")
	req.Header.Set("Authorization", authHeader)

	p, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.UserID != user.ID {
		t.Errorf("UserID = %q, want %q", p.UserID, user.ID)
	}
}

func TestSigV4AuthHeaderWithDateFallback(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-date-fallback",
		DisplayName:      "Date Fallback",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "admin",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo}
	req := httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	req.Host = "localhost:9000"

	// Manually sign using Date header instead of X-Amz-Date
	now := time.Now().UTC()
	timestamp := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	rfcDate := now.Format(time.RFC1123)

	req.Header.Set("Host", req.Host)
	req.Header.Set("Date", rfcDate)
	req.Header.Set("X-Amz-Content-SHA256", EmptyStringHash)

	signedHeadersStr := "date;host;x-amz-content-sha256"
	canonicalRequest := buildCanonicalRequest(req, signedHeadersStr, EmptyStringHash)
	credential := sigv4.AccessKeyID + "/" + date + "/us-east-1/s3/aws4_request"
	stringToSign := buildStringToSign(timestamp, credential, canonicalRequest)
	signature := computeSignature(sigv4.SecretKey, date, "us-east-1", "s3", stringToSign)

	authHeader := sigV4Algorithm +
		" Credential=" + credential +
		", SignedHeaders=" + signedHeadersStr +
		", Signature=" + signature
	req.Header.Set("Authorization", authHeader)

	p, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.UserID != user.ID {
		t.Errorf("UserID = %q, want %q", p.UserID, user.ID)
	}
}

func TestSigV4AuthQueryStringMissingExpires(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-no-expires",
		DisplayName:      "No Expires",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo, Now: time.Now().UTC}
	past := time.Now().UTC().Add(-20 * time.Minute)
	timestamp := past.Format("20060102T150405Z")
	date := past.Format("20060102")
	credential := sigv4.AccessKeyID + "/" + date + "/us-east-1/s3/aws4_request"
	signedHeaders := "host"

	req := httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	req.Host = "localhost:9000"
	req.Header.Set("Host", req.Host)

	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", sigV4Algorithm)
	q.Set("X-Amz-Credential", credential)
	q.Set("X-Amz-Date", timestamp)
	q.Set("X-Amz-SignedHeaders", signedHeaders)
	// Intentionally omit X-Amz-Expires — should be rejected as malformed
	req.URL.RawQuery = q.Encode()

	canonicalRequest := buildCanonicalRequestForQuery(req, signedHeaders, unsignedPayload)
	stringToSign := buildStringToSign(timestamp, credential, canonicalRequest)
	signature := computeSignature(sigv4.SecretKey, date, "us-east-1", "s3", stringToSign)

	q.Set("X-Amz-Signature", signature)
	req.URL.RawQuery = q.Encode()

	_, err = auth.Authenticate(req)
	if err != ErrMalformedToken {
		t.Fatalf("expected ErrMalformedToken for missing X-Amz-Expires, got %v", err)
	}
}

func TestSigV4AuthQueryStringExpiresTooLarge(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-expires-large",
		DisplayName:      "Expires Too Large",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo}
	now := time.Now().UTC()
	timestamp := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	credential := sigv4.AccessKeyID + "/" + date + "/us-east-1/s3/aws4_request"
	signedHeaders := "host"

	req := httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	req.Host = "localhost:9000"
	req.Header.Set("Host", req.Host)

	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", sigV4Algorithm)
	q.Set("X-Amz-Credential", credential)
	q.Set("X-Amz-Date", timestamp)
	q.Set("X-Amz-Expires", "604801") // 1 second over 7 days
	q.Set("X-Amz-SignedHeaders", signedHeaders)
	req.URL.RawQuery = q.Encode()

	canonicalRequest := buildCanonicalRequestForQuery(req, signedHeaders, unsignedPayload)
	stringToSign := buildStringToSign(timestamp, credential, canonicalRequest)
	signature := computeSignature(sigv4.SecretKey, date, "us-east-1", "s3", stringToSign)

	q.Set("X-Amz-Signature", signature)
	req.URL.RawQuery = q.Encode()

	_, err = auth.Authenticate(req)
	if err != ErrMalformedToken {
		t.Fatalf("expected ErrMalformedToken for X-Amz-Expires > 604800, got %v", err)
	}
}

func TestSigV4AuthQueryStringFutureDate(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-future",
		DisplayName:      "Future Date",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo, Now: time.Now().UTC}
	future := time.Now().UTC().Add(30 * time.Minute)
	timestamp := future.Format("20060102T150405Z")
	date := future.Format("20060102")
	credential := sigv4.AccessKeyID + "/" + date + "/us-east-1/s3/aws4_request"
	signedHeaders := "host"

	req := httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	req.Host = "localhost:9000"
	req.Header.Set("Host", req.Host)

	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", sigV4Algorithm)
	q.Set("X-Amz-Credential", credential)
	q.Set("X-Amz-Date", timestamp)
	q.Set("X-Amz-Expires", "3600")
	q.Set("X-Amz-SignedHeaders", signedHeaders)
	req.URL.RawQuery = q.Encode()

	canonicalRequest := buildCanonicalRequestForQuery(req, signedHeaders, unsignedPayload)
	stringToSign := buildStringToSign(timestamp, credential, canonicalRequest)
	signature := computeSignature(sigv4.SecretKey, date, "us-east-1", "s3", stringToSign)

	q.Set("X-Amz-Signature", signature)
	req.URL.RawQuery = q.Encode()

	_, err = auth.Authenticate(req)
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for future timestamp, got %v", err)
	}
}

func TestSigV4AuthMissingHostInSignedHeaders(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-no-host",
		DisplayName:      "No Host",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "admin",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo}
	req := httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	req.Host = "localhost:9000"

	// Sign with a SignedHeaders list that deliberately omits "host"
	SignRequest(req, sigv4.AccessKeyID, sigv4.SecretKey, "us-east-1", "s3", []string{"x-amz-content-sha256", "x-amz-date"}, EmptyStringHash)

	_, err = auth.Authenticate(req)
	if err != ErrMalformedToken {
		t.Fatalf("expected ErrMalformedToken for missing host in SignedHeaders, got %v", err)
	}
}

func TestSigV4AuthQueryStringMissingHostInSignedHeaders(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-query-no-host",
		DisplayName:      "Query No Host",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "member",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo}
	now := time.Now().UTC()
	timestamp := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	credential := sigv4.AccessKeyID + "/" + date + "/us-east-1/s3/aws4_request"
	signedHeaders := "x-amz-content-sha256" // deliberately omits "host"

	req := httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
	req.Host = "localhost:9000"
	req.Header.Set("Host", req.Host)

	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", sigV4Algorithm)
	q.Set("X-Amz-Credential", credential)
	q.Set("X-Amz-Date", timestamp)
	q.Set("X-Amz-Expires", "3600")
	q.Set("X-Amz-SignedHeaders", signedHeaders)
	req.URL.RawQuery = q.Encode()

	canonicalRequest := buildCanonicalRequestForQuery(req, signedHeaders, unsignedPayload)
	stringToSign := buildStringToSign(timestamp, credential, canonicalRequest)
	signature := computeSignature(sigv4.SecretKey, date, "us-east-1", "s3", stringToSign)

	q.Set("X-Amz-Signature", signature)
	req.URL.RawQuery = q.Encode()

	_, err = auth.Authenticate(req)
	if err != ErrMalformedToken {
		t.Fatalf("expected ErrMalformedToken for missing host in SignedHeaders, got %v", err)
	}
}

func TestSigV4AuthInvalidCredentialScope(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	sigv4, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("issue sigv4 credentials: %v", err)
	}

	user := &metadata.User{
		ID:               "user-scope",
		DisplayName:      "Scope Test",
		AccessKeyID:      "bearer_ignored",
		SecretHash:       "hash_ignored",
		SigV4AccessKeyID: sigv4.AccessKeyID,
		SigV4SecretKey:   sigv4.SecretKey,
		Role:             "admin",
		IsActive:         true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auth := &SigV4Authenticator{Repo: sigv4Repo}
	now := time.Now().UTC()
	timestamp := now.Format("20060102T150405Z")
	date := now.Format("20060102")

	tests := []struct {
		name       string
		credential string
	}{
		{"empty_access_key", "/" + date + "/us-east-1/s3/aws4_request"},
		{"bad_date_length", sigv4.AccessKeyID + "/" + date + "01/us-east-1/s3/aws4_request"},
		{"bad_date_chars", sigv4.AccessKeyID + "/abcdefgh/us-east-1/s3/aws4_request"},
		{"empty_region", sigv4.AccessKeyID + "/" + date + "//s3/aws4_request"},
		{"non_s3_service", sigv4.AccessKeyID + "/" + date + "/us-east-1/ec2/aws4_request"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test-bucket/hello.txt", nil)
			req.Host = "localhost:9000"

			authHeader := sigV4Algorithm +
				" Credential=" + tc.credential +
				", SignedHeaders=host" +
				", Signature=dummysignature"
			req.Header.Set("Authorization", authHeader)
			req.Header.Set("Host", req.Host)
			req.Header.Set("X-Amz-Date", timestamp)

			_, err := auth.Authenticate(req)
			if err != ErrMalformedToken {
				t.Fatalf("expected ErrMalformedToken for %s, got %v", tc.name, err)
			}
		})
	}
}
