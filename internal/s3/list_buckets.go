package s3

import (
	"encoding/xml"
	"net/http"
	"sort"

	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

type listAllMyBucketsResult struct {
	XMLName xml.Name        `xml:"ListAllMyBucketsResult"`
	Xmlns   string          `xml:"xmlns,attr"`
	Owner   bucketListOwner `xml:"Owner"`
	Buckets bucketList      `xml:"Buckets"`
}

type bucketListOwner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type bucketList struct {
	Buckets []bucketListEntry `xml:"Bucket"`
}

type bucketListEntry struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

func (h *ObjectHandlers) ListBuckets(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.UserID == "" {
		WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
		return
	}

	buckets, err := h.listBucketsForPrincipal(r, principal)
	if err != nil {
		h.logError("list buckets", err, "", "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	result := listAllMyBucketsResult{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
		Owner: bucketListOwner{
			ID:          principal.UserID,
			DisplayName: principal.DisplayName,
		},
		Buckets: bucketList{Buckets: make([]bucketListEntry, 0, len(buckets))},
	}
	for _, bucket := range buckets {
		result.Buckets.Buckets = append(result.Buckets.Buckets, bucketListEntry{
			Name:         bucket.Name,
			CreationDate: bucket.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(result)
}

func (h *ObjectHandlers) listBucketsForPrincipal(r *http.Request, principal auth.Principal) ([]metadata.Bucket, error) {
	if principal.Role == "admin" {
		return h.Buckets.List(r.Context())
	}

	owned, err := h.Buckets.ListByOwner(r.Context(), principal.UserID)
	if err != nil {
		return nil, err
	}

	if h.Grants == nil {
		return owned, nil
	}

	grantedNames, err := h.Grants.ListBucketNamesWithActiveGrants(r.Context(), principal.UserID)
	if err != nil {
		return nil, err
	}
	if len(grantedNames) == 0 {
		return owned, nil
	}

	granted, err := h.Buckets.ListByNames(r.Context(), grantedNames)
	if err != nil {
		return nil, err
	}

	return mergeBucketsByName(owned, granted), nil
}

func mergeBucketsByName(owned, granted []metadata.Bucket) []metadata.Bucket {
	byName := make(map[string]metadata.Bucket, len(owned)+len(granted))
	for _, b := range owned {
		byName[b.Name] = b
	}
	for _, b := range granted {
		if _, exists := byName[b.Name]; !exists {
			byName[b.Name] = b
		}
	}

	merged := make([]metadata.Bucket, 0, len(byName))
	for _, b := range byName {
		merged = append(merged, b)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].CreatedAt.Equal(merged[j].CreatedAt) {
			return merged[i].Name < merged[j].Name
		}
		return merged[i].CreatedAt.Before(merged[j].CreatedAt)
	})
	return merged
}
