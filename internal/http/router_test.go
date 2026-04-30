package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func TestHealthz(t *testing.T) {
	t.Parallel()

	router := NewRouter(testConfig(), testLogger(), nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("expected JSON content type, got %q", got)
	}

	if body := strings.TrimSpace(recorder.Body.String()); body != `{"status":"ok"}` {
		t.Fatalf("expected health response body, got %q", body)
	}
}

func TestNotFound(t *testing.T) {
	t.Parallel()

	router := NewRouter(testConfig(), testLogger(), nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, recorder.Code)
	}
}

func TestCORSPreflight(t *testing.T) {
	t.Parallel()

	router := NewRouter(testConfig(), testLogger(), nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	request.Header.Set("Origin", "https://dashboard.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.example.com" {
		t.Fatalf("expected CORS allow origin header, got %q", got)
	}

	if got := recorder.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodGet) {
		t.Fatalf("expected allow methods to contain %q, got %q", http.MethodGet, got)
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	t.Parallel()

	router := NewRouter(testConfig(), testLogger(), func(r chi.Router) {
		r.Get("/panic", func(w http.ResponseWriter, r *http.Request) {
			panic("boom")
		})
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, recorder.Code)
	}

	if body := strings.TrimSpace(recorder.Body.String()); body != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("expected recovery response body, got %q", body)
	}
}

func TestAuthIntegration_MissingAuth(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := metadata.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	repo := metadata.NewUserRepository(db)
	authChain := &auth.ChainAuthenticator{
		Authenticators: []auth.Authenticator{
			&auth.BearerAuthenticator{Repo: repo},
		},
	}

	responder := func(w http.ResponseWriter, _ *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if errors.Is(err, auth.ErrMissingAuth) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fbs"`)
			w.WriteHeader(http.StatusUnauthorized)
		} else if errors.Is(err, auth.ErrUnsupportedScheme) {
			w.WriteHeader(http.StatusUnauthorized)
		} else if errors.Is(err, auth.ErrInactiveUser) || errors.Is(err, auth.ErrForbidden) {
			w.WriteHeader(http.StatusForbidden)
		} else if errors.Is(err, auth.ErrInternal) {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
		json.NewEncoder(w).Encode(map[string]string{"error": "auth failed"})
	}

	router := NewRouter(testConfig(), testLogger(), func(r chi.Router) {
		r.Route("/api", func(api chi.Router) {
			api.Use(auth.RequireAuthentication(authChain, responder))
			api.Get("/test", func(w http.ResponseWriter, r *http.Request) {
				p, _ := auth.PrincipalFromContext(r.Context())
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				json.NewEncoder(w).Encode(p)
			})
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("WWW-Authenticate"), `Bearer realm="fbs"`) {
		t.Fatalf("expected WWW-Authenticate header, got %q", rr.Header().Get("WWW-Authenticate"))
	}
}

func TestAuthIntegration_ValidBearerToken(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := metadata.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

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

	repo := metadata.NewUserRepository(db)
	authChain := &auth.ChainAuthenticator{
		Authenticators: []auth.Authenticator{
			&auth.BearerAuthenticator{Repo: repo},
		},
	}

	responder := func(w http.ResponseWriter, _ *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if errors.Is(err, auth.ErrMissingAuth) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fbs"`)
			w.WriteHeader(http.StatusUnauthorized)
		} else if errors.Is(err, auth.ErrUnsupportedScheme) {
			w.WriteHeader(http.StatusUnauthorized)
		} else if errors.Is(err, auth.ErrInactiveUser) || errors.Is(err, auth.ErrForbidden) {
			w.WriteHeader(http.StatusForbidden)
		} else if errors.Is(err, auth.ErrInternal) {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
		json.NewEncoder(w).Encode(map[string]string{"error": "auth failed"})
	}

	router := NewRouter(testConfig(), testLogger(), func(r chi.Router) {
		r.Route("/api", func(api chi.Router) {
			api.Use(auth.RequireAuthentication(authChain, responder))
			api.Get("/test", func(w http.ResponseWriter, r *http.Request) {
				p, _ := auth.PrincipalFromContext(r.Context())
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				json.NewEncoder(w).Encode(p)
			})
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+issued.RawToken)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var p auth.Principal
	if err := json.NewDecoder(rr.Body).Decode(&p); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if p.UserID != "user-test" {
		t.Errorf("UserID = %q, want user-test", p.UserID)
	}
	if p.Role != "member" {
		t.Errorf("Role = %q, want member", p.Role)
	}
	if p.DevMode {
		t.Error("expected DevMode to be false")
	}
}

func TestAuthIntegration_InactiveUser(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := metadata.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	user := &metadata.User{
		ID:          "user-inactive",
		DisplayName: "Inactive User",
		AccessKeyID: "fbsa_inactive_test",
		SecretHash:  "hash",
		Role:        "member",
		IsActive:    false,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := metadata.NewUserRepository(db).Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	repo := metadata.NewUserRepository(db)
	authChain := &auth.ChainAuthenticator{
		Authenticators: []auth.Authenticator{
			&auth.BearerAuthenticator{Repo: repo},
		},
	}

	responder := func(w http.ResponseWriter, _ *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if errors.Is(err, auth.ErrMissingAuth) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fbs"`)
			w.WriteHeader(http.StatusUnauthorized)
		} else if errors.Is(err, auth.ErrUnsupportedScheme) {
			w.WriteHeader(http.StatusUnauthorized)
		} else if errors.Is(err, auth.ErrInactiveUser) || errors.Is(err, auth.ErrForbidden) {
			w.WriteHeader(http.StatusForbidden)
		} else if errors.Is(err, auth.ErrInternal) {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
		json.NewEncoder(w).Encode(map[string]string{"error": "auth failed"})
	}

	router := NewRouter(testConfig(), testLogger(), func(r chi.Router) {
		r.Route("/api", func(api chi.Router) {
			api.Use(auth.RequireAuthentication(authChain, responder))
			api.Get("/test", func(w http.ResponseWriter, r *http.Request) {
				p, _ := auth.PrincipalFromContext(r.Context())
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				json.NewEncoder(w).Encode(p)
			})
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer fbsa_inactive_test.anything")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestAuthIntegration_UnsupportedScheme(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := metadata.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	repo := metadata.NewUserRepository(db)
	authChain := &auth.ChainAuthenticator{
		Authenticators: []auth.Authenticator{
			&auth.BearerAuthenticator{Repo: repo},
		},
	}

	responder := func(w http.ResponseWriter, _ *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if errors.Is(err, auth.ErrMissingAuth) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fbs"`)
			w.WriteHeader(http.StatusUnauthorized)
		} else if errors.Is(err, auth.ErrUnsupportedScheme) {
			w.WriteHeader(http.StatusUnauthorized)
		} else if errors.Is(err, auth.ErrInactiveUser) || errors.Is(err, auth.ErrForbidden) {
			w.WriteHeader(http.StatusForbidden)
		} else if errors.Is(err, auth.ErrInternal) {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
		json.NewEncoder(w).Encode(map[string]string{"error": "auth failed"})
	}

	router := NewRouter(testConfig(), testLogger(), func(r chi.Router) {
		r.Route("/api", func(api chi.Router) {
			api.Use(auth.RequireAuthentication(authChain, responder))
			api.Get("/test", func(w http.ResponseWriter, r *http.Request) {
				p, _ := auth.PrincipalFromContext(r.Context())
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				json.NewEncoder(w).Encode(p)
			})
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if strings.Contains(rr.Header().Get("WWW-Authenticate"), `Bearer realm="fbs"`) {
		t.Error("unsupported scheme should not trigger WWW-Authenticate")
	}
}

func TestAuthIntegration_InternalError(t *testing.T) {
	t.Parallel()

	repo := &failingUserRepo{}
	authChain := &auth.ChainAuthenticator{
		Authenticators: []auth.Authenticator{
			&auth.BearerAuthenticator{Repo: repo},
		},
	}

	responder := func(w http.ResponseWriter, _ *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if errors.Is(err, auth.ErrMissingAuth) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fbs"`)
			w.WriteHeader(http.StatusUnauthorized)
		} else if errors.Is(err, auth.ErrUnsupportedScheme) {
			w.WriteHeader(http.StatusUnauthorized)
		} else if errors.Is(err, auth.ErrInactiveUser) || errors.Is(err, auth.ErrForbidden) {
			w.WriteHeader(http.StatusForbidden)
		} else if errors.Is(err, auth.ErrInternal) {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
		json.NewEncoder(w).Encode(map[string]string{"error": "auth failed"})
	}

	router := NewRouter(testConfig(), testLogger(), func(r chi.Router) {
		r.Route("/api", func(api chi.Router) {
			api.Use(auth.RequireAuthentication(authChain, responder))
			api.Get("/test", func(w http.ResponseWriter, r *http.Request) {
				p, _ := auth.PrincipalFromContext(r.Context())
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				json.NewEncoder(w).Encode(p)
			})
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer fbsa_test.secret")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "database") || strings.Contains(body, "lookup user") {
		t.Error("error body leaks internal details")
	}
}

type failingUserRepo struct{}

func (f *failingUserRepo) Create(_ context.Context, _ *metadata.User) error       { return nil }
func (f *failingUserRepo) GetByID(_ context.Context, _ string) (*metadata.User, error) { return nil, nil }
func (f *failingUserRepo) GetByAccessKeyID(_ context.Context, _ string) (*metadata.User, error) {
	return nil, errors.New("database connection lost")
}
func (f *failingUserRepo) List(_ context.Context) ([]metadata.User, error)        { return nil, nil }
func (f *failingUserRepo) Update(_ context.Context, _ *metadata.User) error       { return nil }
func (f *failingUserRepo) Delete(_ context.Context, _ string) error               { return nil }

func testConfig() config.Config {
	cfg := config.Default()
	cfg.CORSAllowedOrigins = []string{"https://dashboard.example.com"}
	return cfg
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
