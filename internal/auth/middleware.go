package auth

import "net/http"

type UnauthorizedResponder func(w http.ResponseWriter, r *http.Request, err error)

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

func RequireRole(role string, onError UnauthorizedResponder) func(http.Handler) http.Handler {
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
