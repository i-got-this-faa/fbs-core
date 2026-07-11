package management

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

type grantResponse struct {
	ID            string `json:"id"`
	Bucket        string `json:"bucket"`
	GranteeUserID string `json:"grantee_user_id"`
	Action        string `json:"action"`
	KeyPrefix     string `json:"key_prefix"`
	IsActive      bool   `json:"is_active"`
	CreatedBy     string `json:"created_by,omitempty"`
	Note          string `json:"note,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type grantsResponse struct {
	Grants []grantResponse `json:"grants"`
}

type grantEnvelopeResponse struct {
	Grant grantResponse `json:"grant"`
}

type createGrantsResponse struct {
	Grants []grantResponse `json:"grants"`
}

type transferOwnershipRequest struct {
	NewOwnerUserID string `json:"new_owner_user_id"`
}

type transferOwnershipResponse struct {
	Bucket  bucketSummaryResponse `json:"bucket"`
	OwnerID string                `json:"owner_id"`
}

func grantDTO(g metadata.Grant) grantResponse {
	return grantResponse{
		ID:            g.ID,
		Bucket:        g.BucketName,
		GranteeUserID: g.GranteeUserID,
		Action:        g.Action,
		KeyPrefix:     g.KeyPrefix,
		IsActive:      g.IsActive,
		CreatedBy:     g.CreatedBy,
		Note:          g.Note,
		CreatedAt:     formatTime(g.CreatedAt),
		UpdatedAt:     formatTime(g.UpdatedAt),
	}
}

// ListBucketGrants lists grants for a bucket. Admin or bucket owner.
func (h *Handlers) ListBucketGrants(w http.ResponseWriter, r *http.Request) {
	bucket, ok := h.loadBucketForGrantAdmin(w, r)
	if !ok {
		return
	}

	grants, err := h.Grants.ListByBucket(r.Context(), bucket.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to list grants")
		return
	}

	items := make([]grantResponse, 0, len(grants))
	for _, g := range grants {
		items = append(items, grantDTO(g))
	}
	writeJSON(w, http.StatusOK, grantsResponse{Grants: items})
}

// CreateBucketGrants creates one grant row per action. Admin or bucket owner.
func (h *Handlers) CreateBucketGrants(w http.ResponseWriter, r *http.Request) {
	bucket, ok := h.loadBucketForGrantAdmin(w, r)
	if !ok {
		return
	}

	input, ok := decodeCreateGrantRequest(w, r)
	if !ok {
		return
	}

	grantee, ok := h.resolveGrantee(w, r, input.granteeUserID, input.granteeAccessKeyID)
	if !ok {
		return
	}
	if !grantee.IsActive {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "grantee user is inactive")
		return
	}

	if err := validateGrantPrefix(input.keyPrefix); err != nil {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, err.Error())
		return
	}

	principal, _ := auth.PrincipalFromContext(r.Context())
	now := time.Now().UTC()
	created := make([]grantResponse, 0, len(input.actions))

	for _, action := range input.actions {
		grant := &metadata.Grant{
			ID:            uuid.NewString(),
			BucketName:    bucket.Name,
			GranteeUserID: grantee.ID,
			Action:        action,
			KeyPrefix:     input.keyPrefix,
			IsActive:      true,
			CreatedBy:     principal.UserID,
			Note:          input.note,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		result, existed, err := h.Grants.CreateIdempotent(r.Context(), grant)
		if err != nil {
			if errors.Is(err, metadata.ErrInvalidGrantAction) {
				writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid or non-grantable action")
				return
			}
			writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to create grant")
			return
		}
		created = append(created, grantDTO(*result))
		if !existed {
			h.recordActivity(r, "create_grant", bucket.Name, action, 0, result.ID)
		}
	}

	writeJSON(w, http.StatusCreated, createGrantsResponse{Grants: created})
}

// PatchBucketGrant updates prefix, active, or note. Admin or bucket owner.
func (h *Handlers) PatchBucketGrant(w http.ResponseWriter, r *http.Request) {
	bucket, ok := h.loadBucketForGrantAdmin(w, r)
	if !ok {
		return
	}

	grantID := strings.TrimSpace(chi.URLParam(r, "grantID"))
	grant, ok := h.loadGrantOnBucket(w, r, grantID, bucket.Name)
	if !ok {
		return
	}

	input, ok := decodePatchGrantRequest(w, r)
	if !ok {
		return
	}

	if input.keyPrefix != nil {
		if err := validateGrantPrefix(*input.keyPrefix); err != nil {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, err.Error())
			return
		}
		grant.KeyPrefix = *input.keyPrefix
	}
	if input.isActive != nil {
		grant.IsActive = *input.isActive
	}
	if input.note != nil {
		grant.Note = *input.note
	}

	if err := h.Grants.Update(r.Context(), grant); err != nil {
		if errors.Is(err, metadata.ErrGrantNotFound) {
			writeError(w, http.StatusNotFound, errorCodeNotFound, "grant not found")
			return
		}
		if errors.Is(err, metadata.ErrInvalidGrantAction) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid or non-grantable action")
			return
		}
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to update grant")
		return
	}

	updated, err := h.Grants.GetByID(r.Context(), grant.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load updated grant")
		return
	}
	h.recordActivity(r, "update_grant", bucket.Name, updated.Action, 0, updated.ID)
	writeJSON(w, http.StatusOK, grantEnvelopeResponse{Grant: grantDTO(*updated)})
}

// DeleteBucketGrant deletes a grant. Admin or bucket owner.
func (h *Handlers) DeleteBucketGrant(w http.ResponseWriter, r *http.Request) {
	bucket, ok := h.loadBucketForGrantAdmin(w, r)
	if !ok {
		return
	}

	grantID := strings.TrimSpace(chi.URLParam(r, "grantID"))
	grant, ok := h.loadGrantOnBucket(w, r, grantID, bucket.Name)
	if !ok {
		return
	}

	if err := h.Grants.Delete(r.Context(), grant.ID); err != nil {
		if errors.Is(err, metadata.ErrGrantNotFound) {
			writeError(w, http.StatusNotFound, errorCodeNotFound, "grant not found")
			return
		}
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to delete grant")
		return
	}
	h.recordActivity(r, "delete_grant", bucket.Name, grant.Action, 0, grant.ID)
	setNoStoreHeaders(w)
	w.WriteHeader(http.StatusNoContent)
}

// ListMyGrants lists grants for the authenticated principal.
func (h *Handlers) ListMyGrants(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || strings.TrimSpace(principal.UserID) == "" {
		writeError(w, http.StatusUnauthorized, errorCodeUnauthorized, "authentication required")
		return
	}

	grants, err := h.Grants.ListByGrantee(r.Context(), principal.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to list grants")
		return
	}

	items := make([]grantResponse, 0, len(grants))
	for _, g := range grants {
		items = append(items, grantDTO(g))
	}
	writeJSON(w, http.StatusOK, grantsResponse{Grants: items})
}

// ListUserGrants lists grants for a user. Admin only.
func (h *Handlers) ListUserGrants(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(chi.URLParam(r, "userID"))
	if userID == "" {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "user not found")
		return
	}

	if _, err := h.Users.GetByID(r.Context(), userID); errors.Is(err, metadata.ErrUserNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "user not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load user")
		return
	}

	grants, err := h.Grants.ListByGrantee(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to list grants")
		return
	}

	items := make([]grantResponse, 0, len(grants))
	for _, g := range grants {
		items = append(items, grantDTO(g))
	}
	writeJSON(w, http.StatusOK, grantsResponse{Grants: items})
}

// TransferBucketOwnership transfers bucket ownership. Admin or current owner.
func (h *Handlers) TransferBucketOwnership(w http.ResponseWriter, r *http.Request) {
	bucket, ok := h.loadBucketForGrantAdmin(w, r)
	if !ok {
		return
	}

	var req transferOwnershipRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid JSON request body")
		return
	}
	newOwnerID := strings.TrimSpace(req.NewOwnerUserID)
	if newOwnerID == "" {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "new_owner_user_id is required")
		return
	}

	newOwner, err := h.Users.GetByID(r.Context(), newOwnerID)
	if errors.Is(err, metadata.ErrUserNotFound) {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "new owner user not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load new owner")
		return
	}
	if !newOwner.IsActive {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "new owner user is inactive")
		return
	}

	if err := h.Buckets.UpdateOwner(r.Context(), bucket.Name, newOwner.ID); err != nil {
		if errors.Is(err, metadata.ErrBucketNotFound) {
			writeError(w, http.StatusNotFound, errorCodeNotFound, "bucket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to transfer ownership")
		return
	}

	h.recordActivity(r, "transfer_bucket_ownership", bucket.Name, newOwner.ID, 0, "")

	summary, err := h.Management.GetBucketSummary(r.Context(), bucket.Name)
	if err != nil {
		// Ownership already transferred; return the known owner id.
		writeJSON(w, http.StatusOK, transferOwnershipResponse{
			Bucket: bucketSummaryResponse{
				Name:      bucket.Name,
				OwnerID:   newOwner.ID,
				CreatedAt: formatTime(bucket.CreatedAt),
			},
			OwnerID: newOwner.ID,
		})
		return
	}

	writeJSON(w, http.StatusOK, transferOwnershipResponse{
		Bucket:  bucketSummaryDTO(summary),
		OwnerID: newOwner.ID,
	})
}

func (h *Handlers) loadBucketForGrantAdmin(w http.ResponseWriter, r *http.Request) (*metadata.Bucket, bool) {
	bucketName := strings.TrimSpace(chi.URLParam(r, "bucket"))
	if bucketName == "" {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "bucket not found")
		return nil, false
	}

	bucket, err := h.Buckets.GetByName(r.Context(), bucketName)
	if errors.Is(err, metadata.ErrBucketNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "bucket not found")
		return nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load bucket")
		return nil, false
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, errorCodeUnauthorized, "authentication required")
		return nil, false
	}
	if principal.Role != "admin" && bucket.OwnerID != principal.UserID {
		writeError(w, http.StatusForbidden, errorCodeForbidden, "admin or bucket owner required")
		return nil, false
	}
	return bucket, true
}

func (h *Handlers) loadGrantOnBucket(w http.ResponseWriter, r *http.Request, grantID, bucketName string) (*metadata.Grant, bool) {
	if grantID == "" {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "grant not found")
		return nil, false
	}
	grant, err := h.Grants.GetByID(r.Context(), grantID)
	if errors.Is(err, metadata.ErrGrantNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "grant not found")
		return nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load grant")
		return nil, false
	}
	if grant.BucketName != bucketName {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "grant not found")
		return nil, false
	}
	return grant, true
}

func (h *Handlers) resolveGrantee(w http.ResponseWriter, r *http.Request, userID, accessKeyID string) (*metadata.User, bool) {
	userID = strings.TrimSpace(userID)
	accessKeyID = strings.TrimSpace(accessKeyID)

	switch {
	case userID != "" && accessKeyID != "":
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "provide grantee_user_id or grantee_access_key_id, not both")
		return nil, false
	case userID != "":
		user, err := h.Users.GetByID(r.Context(), userID)
		if errors.Is(err, metadata.ErrUserNotFound) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "grantee user not found")
			return nil, false
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load grantee")
			return nil, false
		}
		return user, true
	case accessKeyID != "":
		user, err := h.Users.GetByAccessKeyID(r.Context(), accessKeyID)
		if errors.Is(err, metadata.ErrUserNotFound) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "grantee access key not found")
			return nil, false
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load grantee")
			return nil, false
		}
		return user, true
	default:
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "grantee_user_id or grantee_access_key_id is required")
		return nil, false
	}
}

type createGrantInput struct {
	granteeUserID      string
	granteeAccessKeyID string
	actions            []string
	keyPrefix          string
	note               string
}

func decodeCreateGrantRequest(w http.ResponseWriter, r *http.Request) (createGrantInput, bool) {
	var raw struct {
		GranteeUserID      string   `json:"grantee_user_id"`
		GranteeAccessKeyID string   `json:"grantee_access_key_id"`
		Actions            []string `json:"actions"`
		Action             string   `json:"action"`
		KeyPrefix          string   `json:"key_prefix"`
		Note               string   `json:"note"`
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid JSON request body")
		return createGrantInput{}, false
	}

	actions := make([]string, 0, len(raw.Actions)+1)
	seen := make(map[string]struct{})
	for _, action := range raw.Actions {
		action = strings.TrimSpace(action)
		if action == "" {
			continue
		}
		if !authz.IsGrantable(action) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid or non-grantable action: "+action)
			return createGrantInput{}, false
		}
		if _, ok := seen[action]; ok {
			continue
		}
		seen[action] = struct{}{}
		actions = append(actions, action)
	}
	if single := strings.TrimSpace(raw.Action); single != "" {
		if !authz.IsGrantable(single) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid or non-grantable action: "+single)
			return createGrantInput{}, false
		}
		if _, ok := seen[single]; !ok {
			actions = append(actions, single)
		}
	}
	if len(actions) == 0 {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "at least one action is required")
		return createGrantInput{}, false
	}

	return createGrantInput{
		granteeUserID:      strings.TrimSpace(raw.GranteeUserID),
		granteeAccessKeyID: strings.TrimSpace(raw.GranteeAccessKeyID),
		actions:            actions,
		keyPrefix:          raw.KeyPrefix,
		note:               strings.TrimSpace(raw.Note),
	}, true
}

type patchGrantInput struct {
	keyPrefix *string
	isActive  *bool
	note      *string
}

func decodePatchGrantRequest(w http.ResponseWriter, r *http.Request) (patchGrantInput, bool) {
	var rawFields map[string]json.RawMessage
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&rawFields); err != nil {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid JSON request body")
		return patchGrantInput{}, false
	}

	for field := range rawFields {
		if field != "key_prefix" && field != "is_active" && field != "note" {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid JSON request body")
			return patchGrantInput{}, false
		}
	}
	if len(rawFields) == 0 {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "at least one field is required")
		return patchGrantInput{}, false
	}

	var input patchGrantInput
	if raw, exists := rawFields["key_prefix"]; exists {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || string(raw) == "null" {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "key_prefix must be a string")
			return patchGrantInput{}, false
		}
		input.keyPrefix = &value
	}
	if _, exists := rawFields["is_active"]; exists {
		isActive, ok := decodeBoolField(w, rawFields, "is_active")
		if !ok {
			return patchGrantInput{}, false
		}
		input.isActive = &isActive
	}
	if raw, exists := rawFields["note"]; exists {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || string(raw) == "null" {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "note must be a string")
			return patchGrantInput{}, false
		}
		value = strings.TrimSpace(value)
		input.note = &value
	}
	return input, true
}

func validateGrantPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if strings.Contains(prefix, "\x00") || strings.Contains(prefix, "\n") || strings.Contains(prefix, "\r") {
		return errors.New("invalid key_prefix")
	}
	if strings.HasPrefix(prefix, "/") {
		return errors.New("invalid key_prefix")
	}
	// Folder-style prefixes end with "/"; validate the non-empty stem as a key.
	toCheck := strings.TrimSuffix(prefix, "/")
	if toCheck == "" {
		return errors.New("invalid key_prefix")
	}
	if err := storage.ValidateKey(toCheck); err != nil {
		return errors.New("invalid key_prefix")
	}
	return nil
}
