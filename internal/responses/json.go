package responses

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
)

type JSONOption func(http.ResponseWriter)

func WithNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func WriteJSON(w http.ResponseWriter, statusCode int, payload any, options ...JSONOption) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	for _, option := range options {
		option(w)
	}
	w.WriteHeader(statusCode)
	if _, err := w.Write(body.Bytes()); err != nil {
		slog.Warn("write JSON response", "error", err)
	}
}
