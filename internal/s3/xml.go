package s3

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
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

// Multipart XML types.

type InitiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

type CompleteMultipartUpload struct {
	XMLName xml.Name       `xml:"CompleteMultipartUpload"`
	Parts   []CompletePart `xml:"Part"`
}

type CompletePart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type CompleteMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

// MultipartETag computes the S3-style ETag for a multipart-completed object.
// It concatenates the binary MD5 digests of each part and returns
// hex(md5(concatenation))-len(etags).
func MultipartETag(partETags []string) (string, error) {
	if len(partETags) == 0 {
		return "", fmt.Errorf("no parts")
	}

	hash := md5.New()
	for _, etag := range partETags {
		etag = unquoteETag(etag)
		digest, err := hex.DecodeString(etag)
		if err != nil {
			return "", fmt.Errorf("invalid etag %q: %w", etag, err)
		}
		if _, err := hash.Write(digest); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("%s-%d", hex.EncodeToString(hash.Sum(nil)), len(partETags)), nil
}

func unquoteETag(etag string) string {
	if len(etag) >= 2 && etag[0] == '"' && etag[len(etag)-1] == '"' {
		return etag[1 : len(etag)-1]
	}
	return etag
}
