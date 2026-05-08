package management

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

const (
	defaultObjectListLimit = 100
	maxObjectListLimit     = 1000
)

type Handlers struct {
	Management metadata.ManagementRepository
	Buckets    metadata.BucketRepository
	Objects    metadata.ObjectRepository
	Users      metadata.UserRepository
}

func (h *Handlers) Metrics(w http.ResponseWriter, r *http.Request) {
	metrics, err := h.Management.Metrics(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load metrics")
		return
	}

	writeJSON(w, http.StatusOK, metricsResponse{
		BucketCount:      metrics.BucketCount,
		ObjectCount:      metrics.ObjectCount,
		TotalObjectBytes: metrics.TotalObjectBytes,
		UserCount:        metrics.UserCount,
		ActiveUserCount:  metrics.ActiveUserCount,
	})
}

func (h *Handlers) ListBuckets(w http.ResponseWriter, r *http.Request) {
	summaries, err := h.Management.ListBucketSummaries(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to list buckets")
		return
	}

	buckets := make([]bucketSummaryResponse, 0, len(summaries))
	for _, summary := range summaries {
		buckets = append(buckets, bucketSummaryDTO(summary))
	}

	writeJSON(w, http.StatusOK, bucketsResponse{Buckets: buckets})
}

func (h *Handlers) ListObjects(w http.ResponseWriter, r *http.Request) {
	bucketName := strings.TrimSpace(chi.URLParam(r, "bucket"))
	if !h.ensureBucket(w, r, bucketName) {
		return
	}

	params, err := parseObjectListParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, err.Error())
		return
	}

	objects, commonPrefixes, isTruncated, nextCursor, err := h.listObjects(r, bucketName, params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to list objects")
		return
	}

	writeJSON(w, http.StatusOK, listObjectsResponse{
		Bucket:         bucketName,
		Prefix:         params.prefix,
		Delimiter:      params.delimiter,
		Limit:          params.limit,
		IsTruncated:    isTruncated,
		NextCursor:     nextCursor,
		Objects:        objects,
		CommonPrefixes: commonPrefixes,
	})
}

func (h *Handlers) GetObject(w http.ResponseWriter, r *http.Request) {
	bucketName := strings.TrimSpace(chi.URLParam(r, "bucket"))
	if !h.ensureBucket(w, r, bucketName) {
		return
	}

	key := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	if key == "" {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "object not found")
		return
	}

	obj, err := h.Objects.GetByKey(r.Context(), bucketName, key)
	if errors.Is(err, metadata.ErrObjectNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "object not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load object")
		return
	}

	writeJSON(w, http.StatusOK, objectDetailResponse{Object: objectDetailDTO(*obj)})
}

func (h *Handlers) ListKeys(w http.ResponseWriter, r *http.Request) {
	users, err := h.Users.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to list keys")
		return
	}

	keys := make([]keyResponse, 0, len(users))
	for _, user := range users {
		keys = append(keys, keyDTO(user))
	}

	writeJSON(w, http.StatusOK, keysResponse{Keys: keys})
}

func (h *Handlers) CreateKey(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeCreateKeyRequest(w, r)
	if !ok {
		return
	}

	issued, sigv4Creds, user, err := auth.CreateBearerToken(r.Context(), h.Users, req.displayName, req.role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to create key")
		return
	}

	writeJSON(w, http.StatusCreated, createKeyResponse{
		Key:         keyDTO(*user),
		BearerToken: issued.RawToken,
		SigV4: sigv4Response{
			AccessKeyID: sigv4Creds.AccessKeyID,
			SecretKey:   sigv4Creds.SecretKey,
		},
	})
}

func (h *Handlers) DeleteKey(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "key not found")
		return
	}

	err := h.Users.Delete(r.Context(), id)
	if errors.Is(err, metadata.ErrUserNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "key not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to delete key")
		return
	}

	setNoStoreHeaders(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) ensureBucket(w http.ResponseWriter, r *http.Request, bucketName string) bool {
	if bucketName == "" {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "bucket not found")
		return false
	}

	_, err := h.Buckets.GetByName(r.Context(), bucketName)
	if errors.Is(err, metadata.ErrBucketNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "bucket not found")
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load bucket")
		return false
	}

	return true
}

type objectListParams struct {
	prefix    string
	delimiter string
	cursor    string
	limit     int
}

func parseObjectListParams(r *http.Request) (objectListParams, error) {
	query := r.URL.Query()
	limit := defaultObjectListLimit
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit <= 0 {
			return objectListParams{}, errors.New("limit must be a positive integer")
		}
		limit = parsedLimit
	}
	if limit > maxObjectListLimit {
		limit = maxObjectListLimit
	}

	return objectListParams{
		prefix:    query.Get("prefix"),
		delimiter: query.Get("delimiter"),
		cursor:    query.Get("cursor"),
		limit:     limit,
	}, nil
}

func (h *Handlers) listObjects(r *http.Request, bucketName string, params objectListParams) ([]objectSummaryResponse, []string, bool, string, error) {
	if params.delimiter == "" {
		objects, isTruncated, err := h.Objects.List(r.Context(), bucketName, params.prefix, params.cursor, params.limit)
		if err != nil {
			return nil, nil, false, "", err
		}

		responseObjects := make([]objectSummaryResponse, 0, len(objects))
		for _, obj := range objects {
			responseObjects = append(responseObjects, objectSummaryDTO(obj))
		}

		nextCursor := ""
		if isTruncated && len(objects) > 0 {
			nextCursor = objects[len(objects)-1].Key
		}
		return responseObjects, []string{}, isTruncated, nextCursor, nil
	}

	entries, err := h.Objects.ListDelimited(r.Context(), bucketName, params.prefix, params.cursor, params.delimiter, params.limit)
	if err != nil {
		return nil, nil, false, "", err
	}

	isTruncated := len(entries) > params.limit
	if isTruncated {
		entries = entries[:params.limit]
	}

	objects := make([]objectSummaryResponse, 0, len(entries))
	commonPrefixes := make([]string, 0, len(entries))
	nextCursor := ""
	for _, entry := range entries {
		nextCursor = entry.CursorKey
		if entry.IsPrefix {
			commonPrefixes = append(commonPrefixes, entry.VirtualKey)
			continue
		}
		if entry.Object != nil {
			objects = append(objects, objectSummaryDTO(*entry.Object))
		}
	}
	if !isTruncated {
		nextCursor = ""
	}

	return objects, commonPrefixes, isTruncated, nextCursor, nil
}

type createKeyInput struct {
	displayName string
	role        string
}

func decodeCreateKeyRequest(w http.ResponseWriter, r *http.Request) (createKeyInput, bool) {
	var rawFields map[string]json.RawMessage
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&rawFields); err != nil {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid JSON request body")
		return createKeyInput{}, false
	}

	for field := range rawFields {
		if field != "display_name" && field != "role" {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid JSON request body")
			return createKeyInput{}, false
		}
	}

	displayName, ok := decodeStringField(w, rawFields, "display_name")
	if !ok {
		return createKeyInput{}, false
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "display_name is required")
		return createKeyInput{}, false
	}

	role := "member"
	if _, exists := rawFields["role"]; exists {
		decodedRole, ok := decodeStringField(w, rawFields, "role")
		if !ok {
			return createKeyInput{}, false
		}
		role = strings.TrimSpace(decodedRole)
	}
	if role != "admin" && role != "member" {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "role must be admin or member")
		return createKeyInput{}, false
	}

	return createKeyInput{displayName: displayName, role: role}, true
}

func decodeStringField(w http.ResponseWriter, rawFields map[string]json.RawMessage, field string) (string, bool) {
	rawValue, exists := rawFields[field]
	if !exists {
		return "", true
	}

	var value string
	if err := json.Unmarshal(rawValue, &value); err != nil || string(rawValue) == "null" {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, field+" must be a string")
		return "", false
	}

	return value, true
}
