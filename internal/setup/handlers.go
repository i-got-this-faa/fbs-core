package setup

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/s3compat"
)

const defaultAdminDisplayName = "Initial Admin"

type Handlers struct {
	Bootstrap metadata.BootstrapRepository
	Config    config.Config
}

func RegisterRoutes(r chi.Router, h *Handlers) {
	r.Get("/api/setup/status", h.Status)
	r.Post("/api/setup/bootstrap", h.BootstrapFirstAdmin)
}

func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, errorCodeForbidden, "setup is only available from loopback")
		return
	}

	required, ok := h.bootstrapRequired(w, r)
	if !ok {
		return
	}
	endpoints := h.endpoints(r)

	writeJSON(w, http.StatusOK, statusResponse{
		BootstrapRequired: required,
		Region:            s3compat.Region,
		ManagementURL:     endpoints.managementURL,
		S3URL:             endpoints.s3URL,
	})
}

func (h *Handlers) BootstrapFirstAdmin(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, errorCodeForbidden, "setup is only available from loopback")
		return
	}

	req, ok := decodeBootstrapRequest(w, r)
	if !ok {
		return
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = defaultAdminDisplayName
	}

	issued, sigv4Creds, user, err := auth.CreateFirstAdmin(r.Context(), h.Bootstrap, displayName)
	if errors.Is(err, metadata.ErrUsersAlreadyExist) {
		writeError(w, http.StatusConflict, errorCodeConflict, "users already exist")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to create initial admin")
		return
	}

	endpoints := h.endpoints(r)
	writeJSON(w, http.StatusCreated, bootstrapResponse{
		Key:         keyDTO(*user),
		BearerToken: issued.RawToken,
		SigV4: sigv4Response{
			AccessKeyID: sigv4Creds.AccessKeyID,
			SecretKey:   sigv4Creds.SecretKey,
		},
		Region:        s3compat.Region,
		ManagementURL: endpoints.managementURL,
		S3URL:         endpoints.s3URL,
	})
}

func (h *Handlers) bootstrapRequired(w http.ResponseWriter, r *http.Request) (bool, bool) {
	count, err := h.Bootstrap.UserCount(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to inspect setup state")
		return false, false
	}
	return count == 0, true
}

func decodeBootstrapRequest(w http.ResponseWriter, r *http.Request) (bootstrapRequest, bool) {
	var req bootstrapRequest
	if r.Body == nil {
		return req, true
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return req, true
		}
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid JSON body")
		return bootstrapRequest{}, false
	}

	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid JSON body")
		return bootstrapRequest{}, false
	}

	return req, true
}

func isLoopbackRequest(r *http.Request) bool {
	host := strings.TrimSpace(r.RemoteAddr)
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type endpointURLs struct {
	managementURL string
	s3URL         string
}

func (h *Handlers) endpoints(r *http.Request) endpointURLs {
	baseURL := strings.TrimRight(strings.TrimSpace(h.Config.PublicBaseURL), "/")
	if baseURL == "" {
		baseURL = requestBaseURL(r)
	}

	return endpointURLs{
		managementURL: baseURL + "/api/management",
		s3URL:         baseURL,
	}
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
