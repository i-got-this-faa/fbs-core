package s3

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"

	appmiddleware "github.com/i-got-this-faa/fbs/internal/http/middleware"
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

	requestID := appmiddleware.GetRequestID(r.Context())
	if requestID == "" {
		requestID = "local-0001"
	}
	_ = xml.NewEncoder(w).Encode(S3Error{
		Code:      code,
		Message:   message,
		Resource:  r.URL.Path,
		RequestID: requestID,
	})
}

// Multipart XML types.

type InitiateMultipartUploadResult struct {
	XMLName           xml.Name `xml:"InitiateMultipartUploadResult"`
	Xmlns             string   `xml:"xmlns,attr"`
	Bucket            string   `xml:"Bucket"`
	Key               string   `xml:"Key"`
	UploadID          string   `xml:"UploadId"`
	ChecksumAlgorithm string   `xml:"ChecksumAlgorithm,omitempty"`
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
	Xmlns    string   `xml:"xmlns,attr"`
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

type CopyPartResult struct {
	XMLName      xml.Name `xml:"CopyPartResult"`
	LastModified string   `xml:"LastModified"`
	ETag         string   `xml:"ETag"`
}

type ListMultipartUploadsResult struct {
	XMLName            xml.Name             `xml:"ListMultipartUploadsResult"`
	Xmlns              string               `xml:"xmlns,attr"`
	Bucket             string               `xml:"Bucket"`
	KeyMarker          string               `xml:"KeyMarker,omitempty"`
	UploadIDMarker     string               `xml:"UploadIdMarker,omitempty"`
	NextKeyMarker      string               `xml:"NextKeyMarker,omitempty"`
	NextUploadIDMarker string               `xml:"NextUploadIdMarker,omitempty"`
	MaxUploads         int                  `xml:"MaxUploads"`
	IsTruncated        bool                 `xml:"IsTruncated"`
	Upload             []MultipartUploadEntry `xml:"Upload,omitempty"`
	Delimiter          string               `xml:"Delimiter,omitempty"`
	Prefix             string               `xml:"Prefix,omitempty"`
	CommonPrefixes     []listCommonPrefix   `xml:"CommonPrefixes,omitempty"`
}

type MultipartUploadEntry struct {
	Key          string `xml:"Key"`
	UploadID     string `xml:"UploadId"`
	Initiator    Owner  `xml:"Initiator"`
	Owner        Owner  `xml:"Owner"`
	StorageClass string `xml:"StorageClass"`
	Initiated    string `xml:"Initiated"`
}

type Owner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type ListPartsResult struct {
	XMLName              xml.Name        `xml:"ListPartsResult"`
	Xmlns                string          `xml:"xmlns,attr"`
	Bucket               string          `xml:"Bucket"`
	Key                  string          `xml:"Key"`
	UploadID             string          `xml:"UploadId"`
	Initiator            Owner           `xml:"Initiator"`
	Owner                Owner           `xml:"Owner"`
	StorageClass         string          `xml:"StorageClass"`
	PartNumberMarker     int             `xml:"PartNumberMarker,omitempty"`
	NextPartNumberMarker int             `xml:"NextPartNumberMarker,omitempty"`
	MaxParts             int             `xml:"MaxParts"`
	IsTruncated          bool            `xml:"IsTruncated"`
	Part                 []ListPartsPart `xml:"Part,omitempty"`
}

type ListPartsPart struct {
	PartNumber        int    `xml:"PartNumber"`
	LastModified      string `xml:"LastModified"`
	ETag              string `xml:"ETag"`
	Size              int64  `xml:"Size"`
	ChecksumAlgorithm string `xml:"ChecksumAlgorithm,omitempty"`
}

type GetObjectAttributesResult struct {
	XMLName      xml.Name          `xml:"GetObjectAttributesResult"`
	ETag         string            `xml:"ETag"`
	LastModified string            `xml:"LastModified"`
	ObjectSize   int64             `xml:"ObjectSize"`
	StorageClass string            `xml:"StorageClass"`
	ObjectParts  *ObjectPartsInfo  `xml:"ObjectParts,omitempty"`
	Checksum     *ObjectChecksum   `xml:"Checksum,omitempty"`
}

type ObjectPartsInfo struct {
	PartsCount int `xml:"PartsCount"`
}

type ObjectChecksum struct {
	ChecksumCRC32    string `xml:"ChecksumCRC32,omitempty"`
	ChecksumCRC32C   string `xml:"ChecksumCRC32C,omitempty"`
	ChecksumCRC64NVME string `xml:"ChecksumCRC64NVME,omitempty"`
	ChecksumSHA1     string `xml:"ChecksumSHA1,omitempty"`
	ChecksumSHA256   string `xml:"ChecksumSHA256,omitempty"`
}

type ListVersionsResult struct {
	XMLName       xml.Name           `xml:"ListVersionsResult"`
	Xmlns         string             `xml:"xmlns,attr"`
	Name          string             `xml:"Name"`
	Prefix        string             `xml:"Prefix"`
	KeyMarker     string             `xml:"KeyMarker"`
	NextKeyMarker string             `xml:"NextKeyMarker,omitempty"`
	MaxKeys       int                `xml:"MaxKeys"`
	IsTruncated   bool               `xml:"IsTruncated"`
	Version       []ListVersionsEntry `xml:"Version,omitempty"`
}

type ListVersionsEntry struct {
	Key          string `xml:"Key"`
	VersionID    string `xml:"VersionId"`
	IsLatest     bool   `xml:"IsLatest"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}
