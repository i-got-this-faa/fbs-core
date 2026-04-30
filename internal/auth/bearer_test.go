package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := metadata.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	cleanup := func() {
		db.Close()
		os.Remove(dbPath)
	}
	return db, cleanup
}

func createTestUser(t *testing.T, repo metadata.UserRepository, displayName, role string, isActive bool) (*metadata.User, string) {
	t.Helper()

	issued, err := IssueBearerToken()
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	user := &metadata.User{
		ID:          "user-" + displayName,
		DisplayName: displayName,
		AccessKeyID: issued.AccessKeyID,
		SecretHash:  issued.SecretHash,
		Role:        role,
		IsActive:    isActive,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	if err := repo.Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	return user, issued.RawToken
}

func TestBearerAuthSuccess(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	user, rawToken := createTestUser(t, repo, "Alice", "admin", true)

	ba := &BearerAuthenticator{Repo: repo}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

	p, err := ba.Authenticate(req)
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

func TestBearerAuthMissingHeader(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	ba := &BearerAuthenticator{Repo: metadata.NewUserRepository(db)}
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	_, err := ba.Authenticate(req)
	if err != ErrNotApplicable {
		t.Fatalf("expected ErrNotApplicable, got %v", err)
	}
}

func TestBearerAuthUnsupportedScheme(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	ba := &BearerAuthenticator{Repo: metadata.NewUserRepository(db)}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	_, err := ba.Authenticate(req)
	if err != ErrNotApplicable {
		t.Fatalf("expected ErrNotApplicable, got %v", err)
	}
}

func TestBearerAuthUnsupportedScheme_ChainReturnsErrUnsupportedScheme(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	chain := &ChainAuthenticator{
		Authenticators: []Authenticator{
			&BearerAuthenticator{Repo: metadata.NewUserRepository(db)},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	_, err := chain.Authenticate(req)
	if err != ErrUnsupportedScheme {
		t.Fatalf("expected ErrUnsupportedScheme from chain, got %v", err)
	}
}

func TestBearerAuthMalformedToken(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	ba := &BearerAuthenticator{Repo: metadata.NewUserRepository(db)}

	tests := []string{
		"Bearer notoken",
		"Bearer .",
		"Bearer .secret",
		"Bearer id.",
	}

	for _, auth := range tests {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", auth)
		_, err := ba.Authenticate(req)
		if err != ErrMalformedToken {
			t.Errorf("auth %q: expected ErrMalformedToken, got %v", auth, err)
		}
	}
}

func TestBearerAuthWhitespaceNormalization(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	user, rawToken := createTestUser(t, repo, "Whitespace", "admin", true)

	ba := &BearerAuthenticator{Repo: repo}

	cases := []string{
		"Bearer " + rawToken,           // single space
		"Bearer  " + rawToken,          // double space
		"Bearer\t" + rawToken,          // tab
		" Bearer " + rawToken + " ",   // leading/trailing spaces
		"Bearer\t\t" + rawToken,        // multiple tabs
	}

	for _, auth := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", auth)
		p, err := ba.Authenticate(req)
		if err != nil {
			t.Errorf("auth %q: unexpected error: %v", auth, err)
			continue
		}
		if p.UserID != user.ID {
			t.Errorf("auth %q: UserID = %q, want %q", auth, p.UserID, user.ID)
		}
	}
}

func TestBearerAuthUnknownUser(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	ba := &BearerAuthenticator{Repo: metadata.NewUserRepository(db)}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer fbsa_unknown.secret123")

	_, err := ba.Authenticate(req)
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestBearerAuthInactiveUser(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	_, rawToken := createTestUser(t, repo, "Bob", "member", false)

	ba := &BearerAuthenticator{Repo: repo}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

	_, err := ba.Authenticate(req)
	if err != ErrInactiveUser {
		t.Fatalf("expected ErrInactiveUser, got %v", err)
	}
}

func TestBearerAuthWrongSecret(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	user, _ := createTestUser(t, repo, "Charlie", "admin", true)

	ba := &BearerAuthenticator{Repo: repo}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+user.AccessKeyID+".wrongsecret")

	_, err := ba.Authenticate(req)
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestNoPlaintextSecretInErrors(t *testing.T) {
	t.Parallel()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := metadata.NewUserRepository(db)
	user, rawToken := createTestUser(t, repo, "Dana", "admin", true)
	secret := strings.Split(rawToken, ".")[1]

	ba := &BearerAuthenticator{Repo: repo}

	cases := []struct {
		name   string
		token  string
		wantErr error
	}{
		{"wrong secret", user.AccessKeyID + ".wrongsecret", ErrInvalidCredentials},
		{"unknown user", "fbsa_unknown.secret123", ErrInvalidCredentials},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)

			_, err := ba.Authenticate(req)
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Error("error message leaks the raw secret")
			}
			if strings.Contains(err.Error(), rawToken) {
				t.Error("error message leaks the raw token")
			}
		})
	}
}

type failingUserRepo struct{}

func (f *failingUserRepo) Create(_ context.Context, _ *metadata.User) error         { return nil }
func (f *failingUserRepo) GetByID(_ context.Context, _ string) (*metadata.User, error)   { return nil, nil }
func (f *failingUserRepo) GetByAccessKeyID(_ context.Context, _ string) (*metadata.User, error) {
	return nil, errors.New("database connection lost")
}
func (f *failingUserRepo) List(_ context.Context) ([]metadata.User, error)          { return nil, nil }
func (f *failingUserRepo) Update(_ context.Context, _ *metadata.User) error         { return nil }
func (f *failingUserRepo) Delete(_ context.Context, _ string) error                 { return nil }

func TestBearerAuthInternalError(t *testing.T) {
	t.Parallel()

	ba := &BearerAuthenticator{Repo: &failingUserRepo{}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer fbsa_test.secret")

	_, err := ba.Authenticate(req)
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("expected ErrInternal, got %v", err)
	}
}
