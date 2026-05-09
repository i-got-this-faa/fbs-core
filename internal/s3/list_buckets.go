package s3

import (
	"encoding/xml"
	"net/http"

	"github.com/i-got-this-faa/fbs/internal/auth"
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

	buckets, err := h.Buckets.List(r.Context())
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
