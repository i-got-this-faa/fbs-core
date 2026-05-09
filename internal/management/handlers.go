package management

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/objectops"
	"github.com/i-got-this-faa/fbs/internal/s3compat"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

const (
	defaultObjectListLimit = 100
	maxObjectListLimit     = 1000
	defaultActivityLimit   = 100
	maxActivityLimit       = 500
)

type Handlers struct {
	Management metadata.ManagementRepository
	Buckets    metadata.BucketRepository
	Objects    metadata.ObjectRepository
	Activity   metadata.ActivityRepository
	Users      metadata.UserRepository
	Storage    storage.DiskEngine
	Config     config.Config
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

func (h *Handlers) GetBucket(w http.ResponseWriter, r *http.Request) {
	bucketName := strings.TrimSpace(chi.URLParam(r, "bucket"))
	if bucketName == "" {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "bucket not found")
		return
	}

	summary, err := h.Management.GetBucketSummary(r.Context(), bucketName)
	if errors.Is(err, metadata.ErrBucketNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "bucket not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load bucket")
		return
	}

	writeJSON(w, http.StatusOK, bucketResponse{Bucket: bucketSummaryDTO(summary)})
}

func (h *Handlers) DeleteBucket(w http.ResponseWriter, r *http.Request) {
	bucketName := strings.TrimSpace(chi.URLParam(r, "bucket"))
	if !h.ensureBucket(w, r, bucketName) {
		return
	}

	objects, err := objectops.EmptyBucket(r.Context(), h.Objects, h.Storage, bucketName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to delete bucket objects")
		return
	}

	for _, obj := range objects {
		h.recordActivity(r, "delete_object", bucketName, obj.Key, obj.Size, obj.ETag)
	}

	err = h.Buckets.Delete(r.Context(), bucketName)
	if errors.Is(err, metadata.ErrBucketNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "bucket not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to delete bucket")
		return
	}
	h.recordActivity(r, "force_delete_bucket", bucketName, "", int64(len(objects)), "")

	setNoStoreHeaders(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) EmptyBucket(w http.ResponseWriter, r *http.Request) {
	bucketName := strings.TrimSpace(chi.URLParam(r, "bucket"))
	if !h.ensureBucket(w, r, bucketName) {
		return
	}

	objects, err := objectops.EmptyBucket(r.Context(), h.Objects, h.Storage, bucketName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to empty bucket")
		return
	}

	for _, obj := range objects {
		h.recordActivity(r, "delete_object", bucketName, obj.Key, obj.Size, obj.ETag)
	}
	h.recordActivity(r, "empty_bucket", bucketName, "", int64(len(objects)), "")

	setNoStoreHeaders(w)
	w.WriteHeader(http.StatusNoContent)
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

func (h *Handlers) PatchKey(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "key not found")
		return
	}

	req, ok := decodePatchKeyRequest(w, r)
	if !ok {
		return
	}

	user, err := h.Users.GetByID(r.Context(), id)
	if errors.Is(err, metadata.ErrUserNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "key not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load key")
		return
	}

	if req.displayName != nil {
		user.DisplayName = *req.displayName
	}
	if req.isActive != nil {
		user.IsActive = *req.isActive
	}

	if err := h.Users.Update(r.Context(), user); errors.Is(err, metadata.ErrUserNotFound) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "key not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to update key")
		return
	}

	updated, err := h.Users.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to load updated key")
		return
	}

	writeJSON(w, http.StatusOK, keyEnvelopeResponse{Key: keyDTO(*updated)})
}

func (h *Handlers) ListActivity(w http.ResponseWriter, r *http.Request) {
	if h.Activity == nil {
		writeJSON(w, http.StatusOK, activityResponse{Activity: []activityItemResponse{}})
		return
	}

	params, err := parseActivityParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, err.Error())
		return
	}

	activities, err := h.Activity.List(r.Context(), metadata.ActivityListFilter{
		BucketName: params.bucket,
		Action:     params.action,
		Limit:      params.limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal, "failed to list activity")
		return
	}

	responseItems := make([]activityItemResponse, 0, len(activities))
	for _, activity := range activities {
		responseItems = append(responseItems, activityDTO(activity))
	}

	writeJSON(w, http.StatusOK, activityResponse{Activity: responseItems})
}

func (h *Handlers) ConfigInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, configResponse{
		Region:        s3compat.Region,
		DevMode:       h.Config.DevMode,
		PublicBaseURL: h.Config.PublicBaseURL,
		Limits: configLimitsResponse{
			S3MaxKeys:                 1000,
			S3DeleteObjects:           1000,
			ManagementObjectListLimit: maxObjectListLimit,
			ManagementActivityLimit:   maxActivityLimit,
		},
	})
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

func (h *Handlers) listAllObjects(r *http.Request, bucketName string) ([]metadata.Object, error) {
	var allObjects []metadata.Object
	startAfter := ""
	for {
		objects, isTruncated, err := h.Objects.List(r.Context(), bucketName, "", startAfter, maxObjectListLimit)
		if err != nil {
			return nil, err
		}
		allObjects = append(allObjects, objects...)
		if !isTruncated || len(objects) == 0 {
			return allObjects, nil
		}
		startAfter = objects[len(objects)-1].Key
	}
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

type patchKeyInput struct {
	displayName *string
	isActive    *bool
}

func decodePatchKeyRequest(w http.ResponseWriter, r *http.Request) (patchKeyInput, bool) {
	var rawFields map[string]json.RawMessage
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&rawFields); err != nil {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid JSON request body")
		return patchKeyInput{}, false
	}

	for field := range rawFields {
		if field != "display_name" && field != "is_active" {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "invalid JSON request body")
			return patchKeyInput{}, false
		}
	}
	if len(rawFields) == 0 {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "at least one field is required")
		return patchKeyInput{}, false
	}

	var input patchKeyInput
	if _, exists := rawFields["display_name"]; exists {
		displayName, ok := decodeStringField(w, rawFields, "display_name")
		if !ok {
			return patchKeyInput{}, false
		}
		displayName = strings.TrimSpace(displayName)
		if displayName == "" {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, "display_name must not be empty")
			return patchKeyInput{}, false
		}
		input.displayName = &displayName
	}
	if _, exists := rawFields["is_active"]; exists {
		isActive, ok := decodeBoolField(w, rawFields, "is_active")
		if !ok {
			return patchKeyInput{}, false
		}
		input.isActive = &isActive
	}

	return input, true
}

func decodeBoolField(w http.ResponseWriter, rawFields map[string]json.RawMessage, field string) (bool, bool) {
	rawValue, exists := rawFields[field]
	if !exists {
		return false, true
	}

	var value bool
	if err := json.Unmarshal(rawValue, &value); err != nil || string(rawValue) == "null" {
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequest, field+" must be a boolean")
		return false, false
	}

	return value, true
}

type activityParams struct {
	limit  int
	bucket string
	action string
}

func parseActivityParams(r *http.Request) (activityParams, error) {
	query := r.URL.Query()
	limit := defaultActivityLimit
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit <= 0 {
			return activityParams{}, errors.New("limit must be a positive integer")
		}
		limit = parsedLimit
	}
	if limit > maxActivityLimit {
		limit = maxActivityLimit
	}

	return activityParams{
		limit:  limit,
		bucket: strings.TrimSpace(query.Get("bucket")),
		action: strings.TrimSpace(query.Get("action")),
	}, nil
}

func (h *Handlers) recordActivity(r *http.Request, action, bucketName, key string, size int64, etag string) {
	if h.Activity == nil {
		return
	}

	actorUserID := ""
	if principal, ok := auth.PrincipalFromContext(r.Context()); ok {
		actorUserID = principal.UserID
	}

	_ = h.Activity.Create(r.Context(), &metadata.ObjectActivity{
		ID:          uuid.NewString(),
		Action:      action,
		BucketName:  bucketName,
		ObjectKey:   key,
		Size:        size,
		ETag:        etag,
		ActorUserID: actorUserID,
	})
}
