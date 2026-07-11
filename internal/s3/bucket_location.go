package s3

import (
	"encoding/xml"
	"net/http"

	"github.com/i-got-this-faa/fbs/internal/authz"
)

type locationConstraintResult struct {
	XMLName xml.Name `xml:"LocationConstraint"`
	Xmlns   string   `xml:"xmlns,attr"`
	Value   string   `xml:",chardata"`
}

func (h *ObjectHandlers) GetBucketLocation(w http.ResponseWriter, r *http.Request) {
	bucketName := chiBucketParam(r)
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionListBucket, "", "") {
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(locationConstraintResult{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
		Value: "",
	})
}
