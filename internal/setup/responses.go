package setup

import (
	"net/http"
	"time"

	"github.com/i-got-this-faa/fbs/internal/responses"
)

const (
	errorCodeInvalidRequest = "invalid_request"
	errorCodeForbidden      = "forbidden"
	errorCodeConflict       = "conflict"
	errorCodeInternal       = "internal_error"
)

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

func setNoStoreHeaders(w http.ResponseWriter) {
	responses.WithNoStore(w)
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
