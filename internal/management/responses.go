package management

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/i-got-this-faa/fbs/internal/auth"
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

func writeJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	setJSONHeaders(w)
	w.WriteHeader(statusCode)
	if _, err := w.Write(body.Bytes()); err != nil {
		slog.Warn("write management JSON response", "error", err)
	}
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

func setJSONHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoStoreHeaders(w)
}

func setNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
