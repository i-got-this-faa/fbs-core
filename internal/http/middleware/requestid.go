package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

type requestIDKey struct{}

func GetRequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

func S3Headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		w.Header().Set("x-amz-request-id", id)
		w.Header().Set("x-amz-id-2", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	// crypto/rand.Read never returns an error (Go 1.24+).
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
