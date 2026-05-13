package setup

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const (
	errorCodeInvalidRequest = "invalid_request"
	errorCodeForbidden      = "forbidden"
	errorCodeConflict       = "conflict"
	errorCodeInternal       = "internal_error"
)

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoStoreHeaders(w)
	w.WriteHeader(statusCode)
	if _, err := w.Write(body.Bytes()); err != nil {
		slog.Warn("write setup JSON response", "error", err)
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

func setNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
