package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/i-got-this-faa/fbs/internal/iam"
)

type UnauthorizedResponder func(w http.ResponseWriter, r *http.Request, err error)

// LogInternalErrors wraps onError so that internal authentication failures are
// logged with their cause before onError writes its generic response.
func LogInternalErrors(logger *slog.Logger, onError UnauthorizedResponder) UnauthorizedResponder {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if errors.Is(err, ErrInternal) {
			logger.Error("authentication failed", "error", err, "method", r.Method, "path", r.URL.Path)
		}
		onError(w, r, err)
	}
}

func RequireAuthentication(authenticator Authenticator, onError UnauthorizedResponder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, err := authenticator.Authenticate(r)
			if err != nil {
				onError(w, r, err)
				return
			}
			ctx := WithPrincipal(r.Context(), p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireRole(role iam.Role, onError UnauthorizedResponder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFromContext(r.Context())
			if !ok {
				onError(w, r, ErrUnauthorized)
				return
			}
			if p.Role != role {
				onError(w, r, ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
