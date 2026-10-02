package management

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/responses"
)

const (
	errorCodeInvalidRequest = "invalid_request"
	errorCodeNotFound       = "not_found"
	errorCodeUnauthorized   = "unauthorized"
	errorCodeForbidden      = "forbidden"
	errorCodeInternal       = "internal_error"
)

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	responses.WriteJSON(w, statusCode, payload, responses.WithNoStore)
}

func writeError(w http.ResponseWriter, statusCode int, code, message string) {
	writeJSON(w, statusCode, errorEnvelope{
		Error: errorBody{
			Code:    code,
			Message: message,
		},
	})
}

func WriteAuthError(w http.ResponseWriter, _ *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrMissingAuth):
		w.Header().Set("WWW-Authenticate", `Bearer realm="fbs"`)
		writeError(w, http.StatusUnauthorized, errorCodeUnauthorized, "authentication required")
	case errors.Is(err, auth.ErrUnsupportedScheme),
		errors.Is(err, auth.ErrMalformedToken),
		errors.Is(err, auth.ErrInvalidCredentials),
		errors.Is(err, auth.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, errorCodeUnauthorized, "invalid credentials")
	case errors.Is(err, auth.ErrInactiveUser), errors.Is(err, auth.ErrForbidden):
		writeError(w, http.StatusForbidden, errorCodeForbidden, "admin role required")
	case errors.Is(err, auth.ErrInternal):
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "authentication failed")
	default:
		writeError(w, http.StatusUnauthorized, errorCodeUnauthorized, "invalid credentials")
	}
}

// logger returns h.Logger, or the default logger when none is configured.
func (h *Handlers) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// internalError logs why a request failed and returns a generic 500 so that
// internal details do not reach the client.
func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, message string, err error) {
	h.logger().Error(message, "error", err, "method", r.Method, "path", r.URL.Path)
	writeError(w, http.StatusInternalServerError, errorCodeInternal, message)
}

func setNoStoreHeaders(w http.ResponseWriter) {
	responses.WithNoStore(w)
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
