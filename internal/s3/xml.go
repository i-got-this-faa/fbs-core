package s3

import (
	"encoding/xml"
	"net/http"
)

type S3Error struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId,omitempty"`
}

func WriteS3Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)

	if r.Method == http.MethodHead {
		return
	}

	_ = xml.NewEncoder(w).Encode(S3Error{
		Code:      code,
		Message:   message,
		Resource:  r.URL.Path,
		RequestID: "local-0001",
	})
}
