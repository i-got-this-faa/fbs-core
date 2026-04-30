package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChainAuthenticator(t *testing.T) {
	t.Parallel()

	chain := &ChainAuthenticator{
		Authenticators: []Authenticator{
			&DevAuthenticator{},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	p, err := chain.Authenticate(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.UserID != "dev-user" {
		t.Errorf("UserID = %q, want dev-user", p.UserID)
	}
}

func TestChainAuthenticator_Fallback(t *testing.T) {
	t.Parallel()

	chain := &ChainAuthenticator{
		Authenticators: []Authenticator{},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := chain.Authenticate(req)
	if err != ErrMissingAuth {
		t.Fatalf("expected ErrMissingAuth, got %v", err)
	}
}

func TestChainAuthenticator_PreservesErrors(t *testing.T) {
	t.Parallel()

	chain := &ChainAuthenticator{
		Authenticators: []Authenticator{
			&BearerAuthenticator{},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := chain.Authenticate(req)
	if err != ErrMissingAuth {
		t.Fatalf("expected ErrMissingAuth, got %v", err)
	}
}

func TestChainAuthenticator_RealErrorFromBearer(t *testing.T) {
	t.Parallel()

	chain := &ChainAuthenticator{
		Authenticators: []Authenticator{
			&BearerAuthenticator{},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer malformed")
	_, err := chain.Authenticate(req)
	if err != ErrMalformedToken {
		t.Fatalf("expected ErrMalformedToken, got %v", err)
	}
}

func TestChainAuthenticator_UnsupportedScheme(t *testing.T) {
	t.Parallel()

	chain := &ChainAuthenticator{
		Authenticators: []Authenticator{
			&BearerAuthenticator{},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	_, err := chain.Authenticate(req)
	if err != ErrUnsupportedScheme {
		t.Fatalf("expected ErrUnsupportedScheme, got %v", err)
	}
}

func TestChainAuthenticator_SigV4RequestWouldNotBeBlocked(t *testing.T) {
	t.Parallel()

	// Simulate a future Dev -> Bearer -> SigV4 chain
	// A SigV4 request should NOT be blocked by Bearer returning ErrUnsupportedScheme
	chain := &ChainAuthenticator{
		Authenticators: []Authenticator{
			&BearerAuthenticator{}, // returns ErrNotApplicable for non-Bearer
			// In the future, SigV4Authenticator would go here
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=...")
	_, err := chain.Authenticate(req)
	// With only Bearer in the chain, no authenticator handles it,
	// so the chain returns ErrUnsupportedScheme (not ErrMissingAuth)
	if err != ErrUnsupportedScheme {
		t.Fatalf("expected ErrUnsupportedScheme for unhandled scheme, got %v", err)
	}
}

func TestRequireAuthentication(t *testing.T) {
	t.Parallel()

	authenticator := &DevAuthenticator{}
	responder := func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusUnauthorized)
	}

	handler := RequireAuthentication(authenticator, responder)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFromContext(r.Context())
		if !ok {
			t.Fatal("principal not found in context")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(p.UserID))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if rr.Body.String() != "dev-user" {
		t.Errorf("body = %q, want dev-user", rr.Body.String())
	}
}

func TestRequireAuthentication_Failure(t *testing.T) {
	t.Parallel()

	authenticator := &ChainAuthenticator{Authenticators: []Authenticator{}}
	responder := func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusUnauthorized)
	}

	handler := RequireAuthentication(authenticator, responder)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestRequireRole(t *testing.T) {
	t.Parallel()

	responder := func(w http.ResponseWriter, _ *http.Request, err error) {
		if err == ErrForbidden {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		http.Error(w, err.Error(), http.StatusUnauthorized)
	}

	handler := RequireRole("admin", responder)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("authorized", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(WithPrincipal(req.Context(), Principal{Role: "admin"}))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
	})

	t.Run("forbidden", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(WithPrincipal(req.Context(), Principal{Role: "member"}))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rr.Code)
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rr.Code)
		}
	})
}
