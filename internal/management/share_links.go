package management

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/sharelink"
)

const (
	errorCodeConflict = "conflict"

	// maxShareLinkTTL bounds expires_in_seconds so the expiry cannot overflow.
	maxShareLinkTTL = 100 * 365 * 24 * time.Hour

	// generatedCodeAttempts bounds retries when a random code collides.
	generatedCodeAttempts = 3
)

type createShareLinkRequest struct {
	Bucket                     string `json:"bucket"`
	Key                        string `json:"key"`
	Alias                      string `json:"alias"`
	ExpiresInSeconds           *int64 `json:"expires_in_seconds"`
	ResponseContentDisposition string `json:"response_content_disposition"`
}

type shareLinkResponse struct {
	Code                       string  `json:"code"`
	URL                        string  `json:"url"`
	Bucket                     string  `json:"bucket"`
	Key                        string  `json:"key"`
	ResponseContentDisposition string  `json:"response_content_disposition,omitempty"`
	CreatedBy                  string  `json:"created_by"`
	ExpiresAt                  *string `json:"expires_at"`
	CreatedAt                  string  `json:"created_at"`
}

type shareLinksResponse struct {
	ShareLinks []shareLinkResponse `json:"share_links"`
}

func (h *Handlers) CreateShareLink(w http.ResponseWriter, r *http.Request) {
	req, ttl, ok := decodeCreateShareLinkRequest(w, r)
	if !ok {
		return
	}
	if !h.ensureBucket(w, r, req.Bucket) {
		return
	}
	if _, err := h.Objects.GetByKey(r.Context(), req.Bucket, req.Key); errors.Is(err, metadata.ErrObjectNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "object not found")
		return
	} else if err != nil {
		h.internalError(w, r, "failed to load object", err)
		return
	}

	creatorID, ok := h.shareLinkCreatorID(w, r)
	if !ok {
		return
	}

	now := time.Now().UTC()
	link := &metadata.ShareLink{
		BucketName:                 req.Bucket,
		ObjectKey:                  req.Key,
		ResponseContentDisposition: req.ResponseContentDisposition,
		CreatedBy:                  creatorID,
		CreatedAt:                  now,
	}
	if ttl > 0 {
		expiresAt := now.Add(ttl)
		link.ExpiresAt = &expiresAt
	}

	if err := h.insertShareLink(r, link, req.Alias); errors.Is(err, metadata.ErrShareLinkCodeTaken) {
		writeError(w, http.StatusConflict, errorCodeConflict, "alias is already in use")
		return
	} else if err != nil {
		h.internalError(w, r, "failed to create share link", err)
		return
	}

	writeJSON(w, http.StatusCreated, h.shareLinkDTO(r, *link))
}

func (h *Handlers) ListShareLinks(w http.ResponseWriter, r *http.Request) {
	filter := metadata.ShareLinkListFilter{BucketName: strings.TrimSpace(r.URL.Query().Get("bucket"))}
	links, err := h.ShareLinks.List(r.Context(), filter)
	if err != nil {
		h.internalError(w, r, "failed to list share links", err)
		return
	}

	response := shareLinksResponse{ShareLinks: make([]shareLinkResponse, 0, len(links))}
	for _, link := range links {
		response.ShareLinks = append(response.ShareLinks, h.shareLinkDTO(r, link))
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handlers) DeleteShareLink(w http.ResponseWriter, r *http.Request) {
	err := h.ShareLinks.Delete(r.Context(), chi.URLParam(r, "code"))
	if errors.Is(err, metadata.ErrShareLinkNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "share link not found")
		return
	}
	if err != nil {
		h.internalError(w, r, "failed to delete share link", err)
		return
	}

	setNoStoreHeaders(w)
	w.WriteHeader(http.StatusNoContent)
}

// insertShareLink stores link under alias, or under a fresh random code when
// alias is empty, retrying the rare random collision.
func (h *Handlers) insertShareLink(r *http.Request, link *metadata.ShareLink, alias string) error {
	if alias != "" {
		link.Code = alias
		return h.ShareLinks.Create(r.Context(), link)
	}

	var err error
	for range generatedCodeAttempts {
		link.Code, err = sharelink.GenerateCode()
		if err != nil {
			return err
		}
		err = h.ShareLinks.Create(r.Context(), link)
		if !errors.Is(err, metadata.ErrShareLinkCodeTaken) {
			return err
		}
	}
	return err
}

// shareLinkCreatorID returns the caller's user ID. Links are tied to a stored
// user so that deactivating the user disables their links; principals without
// a user row (dev mode) cannot create them.
func (h *Handlers) shareLinkCreatorID(w http.ResponseWriter, r *http.Request) (string, bool) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, errorCodeUnauthorized, "authentication required")
		return "", false
	}

	_, err := h.Users.GetByID(r.Context(), principal.UserID)
	if errors.Is(err, metadata.ErrUserNotFound) {
		writeError(w, http.StatusForbidden, errorCodeForbidden, "share links require a stored user account")
		return "", false
	}
	if err != nil {
		h.internalError(w, r, "failed to load user", err)
		return "", false
	}
	return principal.UserID, true
}

func decodeCreateShareLinkRequest(w http.ResponseWriter, r *http.Request) (createShareLinkRequest, time.Duration, bool) {
	var req createShareLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid JSON body")
		return createShareLinkRequest{}, 0, false
	}

	req.Bucket = strings.TrimSpace(req.Bucket)
	req.Key = strings.TrimPrefix(req.Key, "/")
	if req.Bucket == "" || req.Key == "" {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "bucket and key are required")
		return createShareLinkRequest{}, 0, false
	}
	if req.Alias != "" {
		if err := sharelink.ValidateAlias(req.Alias); err != nil {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, err.Error())
			return createShareLinkRequest{}, 0, false
		}
	}
	if req.ResponseContentDisposition != "" {
		if _, _, err := mime.ParseMediaType(req.ResponseContentDisposition); err != nil {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "response_content_disposition is invalid")
			return createShareLinkRequest{}, 0, false
		}
	}

	var ttl time.Duration
	if req.ExpiresInSeconds != nil {
		seconds := *req.ExpiresInSeconds
		if seconds <= 0 || seconds > int64(maxShareLinkTTL/time.Second) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "expires_in_seconds must be positive and at most 100 years")
			return createShareLinkRequest{}, 0, false
		}
		ttl = time.Duration(seconds) * time.Second
	}

	return req, ttl, true
}

func (h *Handlers) shareLinkDTO(r *http.Request, link metadata.ShareLink) shareLinkResponse {
	var expiresAt *string
	if link.ExpiresAt != nil {
		formatted := formatTime(*link.ExpiresAt)
		expiresAt = &formatted
	}

	return shareLinkResponse{
		Code:                       link.Code,
		URL:                        strings.TrimRight(h.publicBaseURL(r), "/") + sharelink.Path(link.Code),
		Bucket:                     link.BucketName,
		Key:                        link.ObjectKey,
		ResponseContentDisposition: link.ResponseContentDisposition,
		CreatedBy:                  link.CreatedBy,
		ExpiresAt:                  expiresAt,
		CreatedAt:                  formatTime(link.CreatedAt),
	}
}
