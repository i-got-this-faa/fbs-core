package management

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

// ── shared helpers ────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ── response types ────────────────────────────────────────────────────────────

type metricsResponse struct {
	Buckets      int   `json:"buckets"`
	Objects      int   `json:"objects"`
	StorageBytes int64 `json:"storage_bytes"`
}

type bucketView struct {
	Name      string `json:"name"`
	OwnerID   string `json:"owner_id"`
	CreatedAt string `json:"created_at"`
}

type objectView struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ETag        string `json:"etag"`
	ContentType string `json:"content_type"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type keyView struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	AccessKeyID string `json:"access_key_id"`
	Role        string `json:"role"`
	IsActive    bool   `json:"is_active"`
	CreatedAt   string `json:"created_at"`
}

// ── GET /api/management/metrics ───────────────────────────────────────────────

func handleMetrics(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		buckets, err := d.Buckets.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		var totalObjects int
		var totalBytes int64

		for _, b := range buckets {
			// Fetch all objects for this bucket to count + sum sizes.
			// maxKeys=10000 in a loop is intentionally simple; an LRU cache
			// (F9) or a dedicated SQL aggregate can be layered on later.
			startAfter := ""
			for {
				objs, truncated, err := d.Objects.List(r.Context(), b.Name, "", startAfter, 1000)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "internal error")
					return
				}
				for _, o := range objs {
					totalObjects++
					totalBytes += o.Size
				}
				if !truncated || len(objs) == 0 {
					break
				}
				startAfter = objs[len(objs)-1].Key
			}
		}

		writeJSON(w, http.StatusOK, metricsResponse{
			Buckets:      len(buckets),
			Objects:      totalObjects,
			StorageBytes: totalBytes,
		})
	}
}

// ── GET /api/management/buckets ───────────────────────────────────────────────

func handleListBuckets(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		buckets, err := d.Buckets.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		views := make([]bucketView, 0, len(buckets))
		for _, b := range buckets {
			views = append(views, bucketView{
				Name:      b.Name,
				OwnerID:   b.OwnerID,
				CreatedAt: b.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{"buckets": views})
	}
}

// ── GET /api/management/buckets/{bucket}/objects ──────────────────────────────

func handleListObjects(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bucket := chi.URLParam(r, "bucket")

		// Verify the bucket exists first so we can return a clear 404.
		if _, err := d.Buckets.GetByName(r.Context(), bucket); err != nil {
			if errors.Is(err, metadata.ErrBucketNotFound) {
				writeError(w, http.StatusNotFound, "bucket not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		prefix := r.URL.Query().Get("prefix")
		startAfter := r.URL.Query().Get("start_after")

		maxKeys := 1000
		if s := r.URL.Query().Get("max_keys"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 10000 {
				maxKeys = n
			}
		}

		objs, truncated, err := d.Objects.List(r.Context(), bucket, prefix, startAfter, maxKeys)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		views := make([]objectView, 0, len(objs))
		for _, o := range objs {
			views = append(views, objectView{
				Key:         o.Key,
				Size:        o.Size,
				ETag:        o.ETag,
				ContentType: o.ContentType,
				CreatedAt:   o.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
				UpdatedAt:   o.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"objects":   views,
			"truncated": truncated,
		})
	}
}

// ── GET /api/management/buckets/{bucket}/objects/* ────────────────────────────

func handleGetObject(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bucket := chi.URLParam(r, "bucket")
		key := chi.URLParam(r, "*")

		o, err := d.Objects.GetByKey(r.Context(), bucket, key)
		if err != nil {
			if errors.Is(err, metadata.ErrObjectNotFound) {
				writeError(w, http.StatusNotFound, "object not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		writeJSON(w, http.StatusOK, objectView{
			Key:         o.Key,
			Size:        o.Size,
			ETag:        o.ETag,
			ContentType: o.ContentType,
			CreatedAt:   o.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			UpdatedAt:   o.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
}

// ── GET /api/management/keys ──────────────────────────────────────────────────

func handleListKeys(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		users, err := d.Users.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		views := make([]keyView, 0, len(users))
		for _, u := range users {
			views = append(views, keyView{
				ID:          u.ID,
				DisplayName: u.DisplayName,
				AccessKeyID: u.AccessKeyID,
				Role:        u.Role,
				IsActive:    u.IsActive,
				CreatedAt:   u.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			})
		}
		// SecretHash and SigV4SecretKey are intentionally not included.

		writeJSON(w, http.StatusOK, map[string]any{"keys": views})
	}
}

// ── POST /api/management/keys ─────────────────────────────────────────────────

type createKeyRequest struct {
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type createKeyResponse struct {
	ID              string `json:"id"`
	DisplayName     string `json:"display_name"`
	AccessKeyID     string `json:"access_key_id"`
	SigV4AccessKeyID string `json:"sigv4_access_key_id"`
	RawToken        string `json:"raw_token"`         // Bearer token — shown once only
	SigV4SecretKey  string `json:"sigv4_secret_key"`  // SigV4 secret — shown once only
	Role            string `json:"role"`
}

func handleCreateKey(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.DisplayName == "" {
			writeError(w, http.StatusBadRequest, "display_name is required")
			return
		}
		if req.Role == "" {
			req.Role = "member"
		}

		issued, sigv4Creds, user, err := auth.CreateBearerToken(r.Context(), d.Users, req.DisplayName, req.Role)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		writeJSON(w, http.StatusCreated, createKeyResponse{
			ID:               user.ID,
			DisplayName:      user.DisplayName,
			AccessKeyID:      issued.AccessKeyID,
			SigV4AccessKeyID: sigv4Creds.AccessKeyID,
			RawToken:         issued.RawToken,
			SigV4SecretKey:   sigv4Creds.SecretKey,
			Role:             user.Role,
		})
	}
}

// ── DELETE /api/management/keys/{id} ─────────────────────────────────────────

func handleDeleteKey(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		if err := d.Users.Delete(r.Context(), id); err != nil {
			if errors.Is(err, metadata.ErrUserNotFound) {
				writeError(w, http.StatusNotFound, "key not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
